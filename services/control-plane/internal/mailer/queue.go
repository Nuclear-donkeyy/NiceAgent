package mailer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"niceagent/common/protocol"
)

var (
	ErrInvitationQueueFull   = errors.New("invitation email queue is full")
	ErrInvitationQueueClosed = errors.New("invitation email queue is closed")
)

type InvitationSender interface {
	SendInvitation(ctx context.Context, invitation protocol.Invitation) error
}

type QueueConfig struct {
	Size              int
	Workers           int
	RetryAttempts     int
	RetryInitialDelay time.Duration
}

type QueuedInvitationMailer struct {
	inner             InvitationSender
	logger            *slog.Logger
	queue             chan protocol.Invitation
	stop              chan struct{}
	wg                sync.WaitGroup
	retryAttempts     int
	retryInitialDelay time.Duration
	closeOnce         sync.Once
}

func NewQueuedInvitationMailer(inner InvitationSender, cfg QueueConfig, logger *slog.Logger) *QueuedInvitationMailer {
	if cfg.Size <= 0 {
		cfg.Size = 100
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = 1
	}
	if cfg.RetryInitialDelay < 0 {
		cfg.RetryInitialDelay = 0
	}
	if logger == nil {
		logger = slog.Default()
	}
	mailer := &QueuedInvitationMailer{
		inner:             inner,
		logger:            logger,
		queue:             make(chan protocol.Invitation, cfg.Size),
		stop:              make(chan struct{}),
		retryAttempts:     cfg.RetryAttempts,
		retryInitialDelay: cfg.RetryInitialDelay,
	}
	for i := 0; i < cfg.Workers; i++ {
		mailer.wg.Add(1)
		go mailer.worker(i + 1)
	}
	return mailer
}

func (m *QueuedInvitationMailer) SendInvitation(ctx context.Context, invitation protocol.Invitation) error {
	if m == nil || m.inner == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stop:
		return ErrInvitationQueueClosed
	case m.queue <- invitation:
		return nil
	default:
		return ErrInvitationQueueFull
	}
}

func (m *QueuedInvitationMailer) Close() {
	m.closeOnce.Do(func() {
		close(m.stop)
		m.wg.Wait()
	})
}

func (m *QueuedInvitationMailer) worker(workerID int) {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case invitation := <-m.queue:
			m.deliver(workerID, invitation)
		}
	}
}

func (m *QueuedInvitationMailer) deliver(workerID int, invitation protocol.Invitation) {
	var err error
	for attempt := 1; attempt <= m.retryAttempts; attempt++ {
		err = m.inner.SendInvitation(context.Background(), invitation)
		if err == nil {
			m.logger.Info(
				"invitation email delivered",
				"worker", workerID,
				"attempt", attempt,
				"invitation_id", invitation.ID,
				"organization_id", invitation.OrganizationID,
				"project_id", invitation.ProjectID,
			)
			return
		}
		m.logger.Warn(
			"invitation email delivery failed",
			"worker", workerID,
			"attempt", attempt,
			"invitation_id", invitation.ID,
			"organization_id", invitation.OrganizationID,
			"project_id", invitation.ProjectID,
			"error", err,
		)
		if attempt < m.retryAttempts && m.retryInitialDelay > 0 {
			time.Sleep(m.retryInitialDelay * time.Duration(1<<(attempt-1)))
		}
	}
	m.logger.Error(
		"invitation email delivery exhausted retries",
		"worker", workerID,
		"attempts", m.retryAttempts,
		"invitation_id", invitation.ID,
		"organization_id", invitation.OrganizationID,
		"project_id", invitation.ProjectID,
		"error", err,
	)
}
