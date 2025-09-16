// main.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"notification-hub/shared/pkg/db"
	"notification-hub/shared/pkg/logger"
	"strings"
	"time"

	"notification-hub/shared/pkg/config"
	"notification-hub/shared/pkg/domain"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

/* -------------------- Request/Response DTO -------------------- */

type createNotificationReq struct {
	Channel string   `json:"channel" binding:"required"` // "SMS" | "EMAIL" | "PUSH"
	To      []string `json:"to"      binding:"required"`
	Title   string   `json:"title"`
	Content string   `json:"content" binding:"required"`
	Tenant  string   `json:"tenant"`
}

type createNotificationResp struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}

/* -------------------- GORM MODELS (only in main) -------------------- */

type notificationModel struct {
	ID         string            `gorm:"primaryKey;type:uuid"`
	Tenant     string            `gorm:"type:varchar(64);not null;index:idx_n_tenant_created"`
	Channel    string            `gorm:"type:varchar(16);not null;index"` // SMS|EMAIL|PUSH
	Recipients pq.StringArray    `gorm:"type:text[];not null"`
	Title      *string           `gorm:"type:varchar(200)"`
	Content    string            `gorm:"type:text;not null"`
	Status     string            `gorm:"type:varchar(16);not null;index"` // QUEUED/SENT/FAILED
	Metadata   datatypes.JSONMap `gorm:"type:jsonb;default:'{}'::jsonb"`
	CreatedAt  time.Time         `gorm:"not null;index:idx_n_tenant_created"`
	UpdatedAt  time.Time         `gorm:"not null"`
}

func (notificationModel) TableName() string { return "notification" }

type outboxModel struct {
	ID            string         `gorm:"primaryKey;type:uuid"`
	Payload       datatypes.JSON `gorm:"type:jsonb;not null"`
	Status        string         `gorm:"type:varchar(16);not null;index"` // PENDING|SENT|FAILED
	AttemptCount  int            `gorm:"not null;default:0"`
	NextAttemptAt time.Time      `gorm:"not null"`
	CreatedAt     time.Time      `gorm:"not null"`
	UpdatedAt     time.Time      `gorm:"not null"`
}

func (outboxModel) TableName() string { return "outbox" }

/* -------------------- Mapping (domain -> model) -------------------- */

func toNotificationModel(n domain.Notification) *notificationModel {
	var titlePtr *string
	if t := strings.TrimSpace(n.Title); t != "" {
		titlePtr = &n.Title
	}
	log.Println("time: ", n.CreatedAt)
	return &notificationModel{
		ID:         n.ID,
		Tenant:     n.Tenant,
		Channel:    string(n.Channel),
		Recipients: pq.StringArray(n.Recipients),
		Title:      titlePtr,
		Content:    n.Content,
		Status:     "QUEUED",
		Metadata:   datatypes.JSONMap(n.Metadata),
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.CreatedAt,
	}
}

func toOutboxModel(id string, payload datatypes.JSON) *outboxModel {
	now := time.Now()
	logger.Info("outbox time: %v", now)
	return &outboxModel{
		ID:            id,
		Payload:       payload,
		Status:        "QUEUED",
		AttemptCount:  0,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

/* -------------------- MAIN -------------------- */

func main() {
	cfg := config.MustLoad()

	// Open GORM (Postgres) — all config ở đây, KHÔNG trong domain
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

	r := gin.New()
	r.Use(gin.Logger())

	// CORS dev
	c := cors.DefaultConfig()
	c.AllowAllOrigins = true
	c.AllowHeaders = []string{"Content-Type", "Authorization"}
	c.AllowMethods = []string{"GET", "POST", "OPTIONS"}
	r.Use(cors.New(c))

	// Health
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// Create notification
	r.POST("/notifications", func(c *gin.Context) {
		var req createNotificationReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
			return
		}
		if err := validateCreateReq(req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		id := uuid.NewString()
		now := time.Now()
		noti := domain.Notification{
			ID:         id,
			Channel:    domain.Channel(strings.ToUpper(req.Channel)),
			Tenant:     firstNonEmpty(req.Tenant, "default"),
			Recipients: req.To,
			Title:      req.Title,
			Content:    req.Content,
			Metadata:   map[string]any{},
			CreatedAt:  now,
		}

		evt := domain.DispatchEvent{
			EventID:        uuid.NewString(),
			NotificationID: noti.ID,
			Channel:        noti.Channel,
			Tenant:         noti.Tenant,
			To:             noti.Recipients,
			Title:          noti.Title,
			Content:        noti.Content,
			Metadata:       noti.Metadata,
			CreatedAt:      now,
			Attempt:        0,
		}
		payload, _ := json.Marshal(evt)

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		if err := insertNotificationAndOutbox(ctx, gdb, noti, payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue: " + err.Error()})
			return
		}

		c.JSON(http.StatusAccepted, createNotificationResp{Status: "QUEUED", ID: id})
	})

	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		panic(err)
	}
}

/* -------------------- TX: insert notification + outbox -------------------- */

func insertNotificationAndOutbox(ctx context.Context, db *gorm.DB, n domain.Notification, payload []byte) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) notifications
		model := toNotificationModel(n)
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		// 2) outbox
		out := toOutboxModel(n.ID, payload)
		if err := tx.Create(out).Error; err != nil {
			return err
		}
		return nil
	})
}

/* -------------------- Helpers -------------------- */

func validateCreateReq(req createNotificationReq) error {
	ch := strings.ToUpper(strings.TrimSpace(req.Channel))
	switch ch {
	case "SMS", "EMAIL", "PUSH":
	default:
		return errors.New(`channel must be one of: "SMS","EMAIL","PUSH"`)
	}
	if len(req.To) == 0 {
		return errors.New("to must contain at least one recipient")
	}
	if strings.TrimSpace(req.Content) == "" {
		return errors.New("content is required")
	}
	if (ch == "EMAIL" || ch == "PUSH") && strings.TrimSpace(req.Title) == "" {
		return errors.New("title is required for EMAIL/PUSH")
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
