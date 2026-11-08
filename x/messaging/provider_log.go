package messaging

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/birdordev/plumego/log"
)

// LogSMSProvider is a development/testing provider that logs SMS messages
// instead of sending them. Useful for local development and integration tests.
type LogSMSProvider struct {
	logger log.StructuredLogger
	seq    atomic.Int64
}

// NewLogSMSProvider creates a log-only SMS provider.
func NewLogSMSProvider(logger log.StructuredLogger) *LogSMSProvider {
	return &LogSMSProvider{logger: logger}
}

func (p *LogSMSProvider) Name() string { return "log-sms" }

func (p *LogSMSProvider) Send(_ context.Context, msg SMSMessage) (*SMSResult, error) {
	id := fmt.Sprintf("log-sms-%d", p.seq.Add(1))
	if p.logger != nil {
		p.logger.Info("sms sent (log provider)", log.Fields{"id": id, "to": msg.To, "body_len": len(msg.Body)})
	}
	return &SMSResult{ProviderID: id}, nil
}

// LogEmailProvider is a development/testing provider that logs email messages.
type LogEmailProvider struct {
	logger log.StructuredLogger
	seq    atomic.Int64
}

// NewLogEmailProvider creates a log-only email provider.
func NewLogEmailProvider(logger log.StructuredLogger) *LogEmailProvider {
	return &LogEmailProvider{logger: logger}
}

func (p *LogEmailProvider) Name() string { return "log-email" }

func (p *LogEmailProvider) Send(_ context.Context, msg EmailMessage) (*EmailResult, error) {
	id := fmt.Sprintf("log-email-%d", p.seq.Add(1))
	if p.logger != nil {
		p.logger.Info("email sent (log provider)", log.Fields{"id": id, "to": msg.To, "subject": msg.Subject, "html": msg.HTML})
	}
	return &EmailResult{MessageID: id}, nil
}
