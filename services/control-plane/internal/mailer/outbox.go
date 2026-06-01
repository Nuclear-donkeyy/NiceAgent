package mailer

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"niceagent/common/protocol"
)

type InvitationOutbox interface {
	EnqueueInvitationEmail(invitation protocol.Invitation, maxAttempts int) (protocol.InvitationEmailDelivery, error)
	ClaimDueInvitationEmails(limit int, lockedBy string, lockUntil time.Time) []protocol.InvitationEmailDelivery
	MarkInvitationEmailSent(deliveryID string) error
	MarkInvitationEmailFailed(deliveryID, lastError string, nextAttemptAt *time.Time, terminal bool) error
}

type DurableQueueConfig struct {
	Workers           int
	RetryAttempts     int
	RetryInitialDelay time.Duration
	PollInterval      time.Duration
	LockTTL           time.Duration
	WorkerID          string
}

type OutboxInvitationMailer struct {
	inner             InvitationSender
	outbox            InvitationOutbox
	logger            *slog.Logger
	stop              chan struct{}
	wg                sync.WaitGroup
	retryAttempts     int
	retryInitialDelay time.Duration
	pollInterval      time.Duration
	lockTTL           time.Duration
	workerID          string
	closeOnce         sync.Once
}

func NewOutboxInvitationMailer(inner InvitationSender, outbox InvitationOutbox, cfg DurableQueueConfig, logger *slog.Logger) *OutboxInvitationMailer {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = 1
	}
	if cfg.RetryInitialDelay < 0 {
		cfg.RetryInitialDelay = 0
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 30 * time.Second
	}
	if strings.TrimSpace(cfg.WorkerID) == "" {
		cfg.WorkerID = "control-plane"
	}
	if logger == nil {
		logger = slog.Default()
	}
	mailer := &OutboxInvitationMailer{
		inner:             inner,
		outbox:            outbox,
		logger:            logger,
		stop:              make(chan struct{}),
		retryAttempts:     cfg.RetryAttempts,
		retryInitialDelay: cfg.RetryInitialDelay,
		pollInterval:      cfg.PollInterval,
		lockTTL:           cfg.LockTTL,
		workerID:          cfg.WorkerID,
	}
	for i := 0; i < cfg.Workers; i++ {
		mailer.wg.Add(1)
		go mailer.worker(i + 1)
	}
	return mailer
}

func (m *OutboxInvitationMailer) SendInvitation(ctx context.Context, invitation protocol.Invitation) error {
	if m == nil || m.outbox == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := m.outbox.EnqueueInvitationEmail(invitation, m.retryAttempts)
	return err
}

func (m *OutboxInvitationMailer) Close() {
	m.closeOnce.Do(func() {
		close(m.stop)
		m.wg.Wait()
	})
}

func (m *OutboxInvitationMailer) worker(workerIndex int) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		m.deliverDue(workerIndex)
		select {
		case <-m.stop:
			return
		case <-ticker.C:
		}
	}
}

func (m *OutboxInvitationMailer) deliverDue(workerIndex int) {
	if m == nil || m.inner == nil || m.outbox == nil {
		return
	}
	lockedBy := m.workerID
	if workerIndex > 0 {
		lockedBy = lockedBy + "-" + strconv.Itoa(workerIndex)
	}
	for _, delivery := range m.outbox.ClaimDueInvitationEmails(1, lockedBy, time.Now().UTC().Add(m.lockTTL)) {
		m.deliver(workerIndex, delivery)
	}
}

func (m *OutboxInvitationMailer) deliver(workerIndex int, delivery protocol.InvitationEmailDelivery) {
	err := m.inner.SendInvitation(context.Background(), delivery.Invitation)
	if err == nil {
		if markErr := m.outbox.MarkInvitationEmailSent(delivery.ID); markErr != nil {
			m.logger.Warn("invitation email outbox sent but mark failed", "worker", workerIndex, "delivery_id", delivery.ID, "invitation_id", delivery.InvitationID, "error", markErr)
		}
		m.logger.Info("invitation email outbox delivered", "worker", workerIndex, "delivery_id", delivery.ID, "invitation_id", delivery.InvitationID, "attempt", delivery.Attempts)
		return
	}
	terminal := delivery.Attempts >= delivery.MaxAttempts
	var nextAttemptAt *time.Time
	if !terminal {
		next := time.Now().UTC().Add(retryDelay(m.retryInitialDelay, delivery.Attempts))
		nextAttemptAt = &next
	}
	if markErr := m.outbox.MarkInvitationEmailFailed(delivery.ID, err.Error(), nextAttemptAt, terminal); markErr != nil {
		m.logger.Warn("invitation email outbox failure mark failed", "worker", workerIndex, "delivery_id", delivery.ID, "invitation_id", delivery.InvitationID, "error", markErr)
	}
	m.logger.Warn("invitation email outbox delivery failed", "worker", workerIndex, "delivery_id", delivery.ID, "invitation_id", delivery.InvitationID, "attempt", delivery.Attempts, "max_attempts", delivery.MaxAttempts, "terminal", terminal, "error", err)
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		return 0
	}
	if attempt <= 1 {
		return base
	}
	return base * time.Duration(1<<(attempt-1))
}
