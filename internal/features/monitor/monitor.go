package monitor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/robfig/cron/v3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// HealthStatus represents the current health state
type HealthStatus string

const (
	StatusUp       HealthStatus = "up"
	StatusDown     HealthStatus = "down"
	StatusSlow     HealthStatus = "slow"
	StatusRecovery HealthStatus = "recovery"
)

// MonitorService handles system health monitoring
type MonitorService struct {
	waClient     *whatsmeow.Client
	healthURL    string
	alertPhone   string
	cron         *cron.Cron
	lastStatus   HealthStatus
	downSince    time.Time
	alertSent    bool
	slowCount    int
}

// NewMonitorService creates a new monitor service
func NewMonitorService(waClient *whatsmeow.Client, webURL, alertPhone string) *MonitorService {
	return &MonitorService{
		waClient:   waClient,
		healthURL:  fmt.Sprintf("%s/api/health", webURL),
		alertPhone: alertPhone,
		cron:       cron.New(),
		lastStatus: StatusUp,
	}
}

// Start starts the monitor cron job (runs every 5 minutes)
func (s *MonitorService) Start() error {
	_, err := s.cron.AddFunc("*/5 * * * *", func() {
		s.checkHealth()
	})
	if err != nil {
		return fmt.Errorf("failed to schedule monitor: %w", err)
	}

	s.cron.Start()
	log.Println("✅ [MONITOR] Health check scheduler started (runs every 5 mins)")
	return nil
}

// Stop stops the monitor cron job
func (s *MonitorService) Stop() {
	s.cron.Stop()
}

// checkHealth performs a health check and sends alerts if needed
func (s *MonitorService) checkHealth() {
	ctx := context.Background()
	now := time.Now()

	start := time.Now()
	status, latency := s.pingHealth()
	log.Printf("🏥 [MONITOR] Health check: %s (latency: %dms)", status, latency)

	// Handle status transitions
	switch status {
	case StatusDown:
		if s.lastStatus != StatusDown {
			s.downSince = now
			s.alertSent = false
		}
		// Send alert after 5 minutes of downtime
		if !s.alertSent && now.Sub(s.downSince) > 5*time.Minute {
			s.sendAlert(ctx, StatusDown, latency)
			s.alertSent = true
		}

	case StatusSlow:
		s.slowCount++
		// Alert after 3 consecutive slow checks
		if s.slowCount >= 3 && s.lastStatus != StatusSlow {
			s.sendAlert(ctx, StatusSlow, latency)
		}

	case StatusUp:
		// Send recovery alert if was down
		if s.lastStatus == StatusDown && s.alertSent {
			s.sendAlert(ctx, StatusRecovery, latency)
		}
		s.slowCount = 0
		s.alertSent = false
	}

	s.lastStatus = status
	_ = start // Used for latency calculation
}

// pingHealth performs the actual health check
func (s *MonitorService) pingHealth() (HealthStatus, int) {
	client := &http.Client{Timeout: 10 * time.Second}
	
	start := time.Now()
	resp, err := client.Get(s.healthURL)
	latency := int(time.Since(start).Milliseconds())

	if err != nil {
		log.Printf("❌ [MONITOR] Health check failed: %v", err)
		return StatusDown, latency
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return StatusDown, latency
	}

	if latency > 5000 {
		return StatusSlow, latency
	}

	return StatusUp, latency
}

// sendAlert sends a WhatsApp alert
func (s *MonitorService) sendAlert(ctx context.Context, status HealthStatus, latency int) {
	if s.waClient == nil || s.alertPhone == "" {
		log.Printf("⚠️ [MONITOR] Cannot send alert: client or phone not configured")
		return
	}

	var message string
	timestamp := time.Now()

	switch status {
	case StatusRecovery:
		message = fmt.Sprintf("✅ *SISTEM PULIH*\n\n🕐 %s\n\nSistem Valpro Intertech kembali online setelah mengalami gangguan.",
			timestamp.Format("02 Jan 2006 15:04"))
	case StatusSlow:
		message = fmt.Sprintf("⚠️ *SISTEM LAMBAT*\n\n🕐 %s\n⏱️ Latency: %dms\n\nRespon sistem lebih lambat dari normal (> 5 detik).",
			timestamp.Format("02 Jan 2006 15:04"), latency)
	case StatusDown:
		message = fmt.Sprintf("🚨 *SISTEM DOWN*\n\n🕐 %s\n⏱️ Down sejak: %s\n\nSistem tidak dapat diakses. Tim teknis sedang menangani.",
			timestamp.Format("02 Jan 2006 15:04"), s.downSince.Format("15:04"))
	default:
		return
	}

	jid := types.NewJID(s.alertPhone, types.DefaultUserServer)
	_, err := s.waClient.SendMessage(ctx, jid, &waProto.Message{
		Conversation: proto.String(message),
	})
	if err != nil {
		log.Printf("❌ [MONITOR] Failed to send alert: %v", err)
	} else {
		log.Printf("📤 [MONITOR] Alert sent for status: %s", status)
	}
}

// GetStatus returns current health status (for API)
func (s *MonitorService) GetStatus() map[string]interface{} {
	return map[string]interface{}{
		"lastStatus": s.lastStatus,
		"downSince":  s.downSince,
		"alertSent":  s.alertSent,
		"slowCount":  s.slowCount,
	}
}
