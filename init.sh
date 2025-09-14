# 1) create project root and enter it
mkdir -p notification-hub && cd notification-hub

# 2) create folders
mkdir -p shared/pkg/{domain,db,config}
for svc in gateway outbox-poller sms-service email-service push-service; do
  mkdir -p "$svc/cmd/$svc"
done

# 3) init modules
( cd shared && go mod init github.com/yourorg/notification-hub/shared )
for svc in gateway outbox-poller sms-service email-service push-service; do
  ( cd "$svc" && go mod init github.com/yourorg/notification-hub/$svc )
done

# 4) add a tiny main so "go run" works (gateway example)
cat > gateway/cmd/gateway/main.go <<'EOF'
package main
import "log"
func main(){ log.Println("gateway up"); select{} }
EOF

# 5) create workspace
go work init ./shared ./gateway ./outbox-poller ./sms-service ./email-service ./push-service

# 6) tidy
for d in shared gateway outbox-poller sms-service email-service push-service; do
  ( cd "$d" && go mod tidy )
done
