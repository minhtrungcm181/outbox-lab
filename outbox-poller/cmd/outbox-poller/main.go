package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"notification-hub/shared/pkg/config"
	"notification-hub/shared/pkg/db"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"gorm.io/gorm"
)

type OutboxRow struct {
	ID           string    `gorm:"column:id"`
	Payload      []byte    `gorm:"column:payload"`
	status       string    `gorm:"column:status"`
	AttemptCount int       `gorm:"column:attempt"`
	nextAtteptAt time.Time `gorm:"column:next_attept_at"`
	createdAt    time.Time `gorm:"column:created_at"`
	updatedAt    time.Time `gorm:"column:updated_at"`
}

type TemporaryError struct{ error }

type dispatchEventLite struct {
	NotificationID string `json:"NotificationID"` // đổi thành "notificationId" nếu payload là camelCase
}

func NewKafkaPublisher() (sarama.SyncProducer, error) {
	broker := config.MustLoad().Kafka
	brokers := []string{}
	brokers = append(brokers, broker)
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	cfg.Producer.Idempotent = true
	cfg.Net.MaxOpenRequests = 1
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 5
	prod, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		log.Println("NewKafkaPublisher-err: {}", err)
		return nil, err
	}
	return prod, nil
}

func claimBatch(ctx context.Context, db *gorm.DB, limit int, lease time.Duration) ([]OutboxRow, error) {
	var rows []OutboxRow
	leaseSeconds := int(lease / time.Second)
	raw := `
		WITH cte AS (
		  SELECT id
		  FROM outbox
		  WHERE status = 'QUEUED'
			AND next_attempt_at <= now()
		  ORDER BY next_attempt_at
		  LIMIT ?
		  FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox o
		SET next_attempt_at = now() + (? * interval '1 second'),
			updated_at = now()
		FROM cte
		WHERE o.id = cte.id
		RETURNING o.id, o.payload, o.status, o.attempt_count, o.next_attempt_at, o.created_at, o.updated_at;
		`
	tx := db.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err := tx.Raw(raw, limit, leaseSeconds).Scan(&rows).Error; err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return rows, nil
}
func pollLoop(ctx context.Context, db *gorm.DB, batch int, lease time.Duration, tick time.Duration, publish func(ctx context.Context, payload []byte) error) error {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rows, err := claimBatch(ctx, db, batch, lease)
		if err != nil {
			// log: claim lỗi → nghỉ một nhịp rồi thử lại
			fmt.Print("could not claim batch: {}", err)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		if len(rows) == 0 {
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		for _, r := range rows {
			_ = handleOne(ctx, db, r, publish) // có thể log lỗi nếu muốn
		}
	}
}
func handleOne(ctx context.Context, db *gorm.DB, r OutboxRow, publish func(context.Context, []byte) error) error {
	err := publish(ctx, r.Payload) // đúng 1 lần/claim

	switch {
	case err == nil:
		// success → PUBLISHED + SENT (tuỳ bạn có cần update notifications không)
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`UPDATE outbox SET status='PUBLISHED', updated_at=now() WHERE id=$1`, r.ID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE notification SET status='SENT', updated_at=now() WHERE id=$1`, r.ID).Error; err != nil {
				return err
			}
			return nil
		})

	case isRetryable(err):
		// retryable → backoff rồi thử lại lần khác

		next := time.Now().Add(backoff(r.AttemptCount))
		return db.WithContext(ctx).Exec(`
			UPDATE outbox
			SET attempt_count = attempt_count + 1,
			    status='PENDING',
			    next_attempt_at = $2,
			    updated_at = now()
			WHERE id = $1
		`, r.ID, next).Error

	default:
		// non-retryable → FAILED (+ FAILED notifications tuỳ nhu cầu)
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`UPDATE outbox SET status='FAILED', updated_at=now() WHERE id=$1`, r.ID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE notification SET status='FAILED', updated_at=now() WHERE id=$1`, r.ID).Error; err != nil {
				return err
			}
			return nil
		})
	}
}
func isRetryable(err error) bool {
	var te *TemporaryError
	return errors.As(err, &te)
}

func backoff(attempt int) time.Duration {
	// 1,2,4,8,... giây + jitter, tối đa 5 phút
	sec := 1 << attempt
	if sec > 300 {
		sec = 300
	}
	jitter := time.Duration(100+randInt(0, 400)) * time.Millisecond
	return time.Duration(sec)*time.Second + jitter
}

// tách hàm random để dễ mock test
func randInt(min, max int) int {
	return min + int(time.Now().UnixNano()%int64(max-min+1))
}

func PublishWithProducer(prod sarama.SyncProducer) func(ctx context.Context, payload []byte) error {
	return func(ctx context.Context, payload []byte) error {
		log.Println("publishing payload:", string(payload))
		var ev dispatchEventLite
		if err := json.Unmarshal(payload, &ev); err != nil || strings.TrimSpace(ev.NotificationID) == "" {
			return fmt.Errorf("invalid payload: missing NotificationID: %w", err)
		}
		msg := &sarama.ProducerMessage{
			Topic: "notifications",
			Key:   sarama.StringEncoder(ev.NotificationID),
			Value: sarama.ByteEncoder(payload),
		}
		done := make(chan error, 1)
		go func() {
			_, _, err := prod.SendMessage(msg)
			log.Println("published message: {}", err)
			done <- err
		}()
		select {
		case <-ctx.Done():
			return &TemporaryError{ctx.Err()}
		case err := <-done:
			if err != nil {
				return &TemporaryError{err}
			}
			return nil
		}
	}
}

func main() {
	gdb, err := db.Open()
	if err != nil {
		log.Fatal(err)
	}

	// unwrap sql.DB for pool config
	sqlDB, err := gdb.DB()
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	prod, err := NewKafkaPublisher()
	//if err != nil {
	//	log.Fatal(err)
	//}
	publishFn := PublishWithProducer(prod)

	if err := pollLoop(ctx, gdb, 100, 30*time.Second, 1*time.Second, publishFn); err != nil {
		log.Fatal(err)
	}

}
