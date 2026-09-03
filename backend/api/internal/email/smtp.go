package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig follows the SMTP settings used by the reference application.
// UseTLS means STARTTLS, which is the normal mode for port 587.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	UseTLS   bool
	Timeout  time.Duration
}

type SMTPSender struct {
	cfg SMTPConfig
}

func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	if cfg.Port <= 0 {
		cfg.Port = 587
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &SMTPSender{cfg: cfg}
}

// SendGeneric sends a UTF-8 multipart/alternative message. The plain-text
// part is always retained so transactional mail works in text-only clients.
func (s *SMTPSender) SendGeneric(ctx context.Context, to, subject, plainBody, htmlBody string) error {
	if s == nil {
		return fmt.Errorf("smtp sender is not configured")
	}
	to = strings.TrimSpace(to)
	subject = strings.TrimSpace(subject)
	plainBody = strings.TrimSpace(plainBody)
	htmlBody = strings.TrimSpace(htmlBody)
	if to == "" || subject == "" || plainBody == "" {
		return fmt.Errorf("smtp sender is not configured")
	}
	if htmlBody == "" {
		htmlBody = `<pre style="white-space:pre-wrap;font-family:ui-monospace,Menlo,Consolas,monospace;">` + html.EscapeString(plainBody) + `</pre>`
	}
	return s.sendMultipart(ctx, to, subject, plainBody, htmlBody)
}

func (s *SMTPSender) sendMultipart(ctx context.Context, to, subject, plainBody, htmlBody string) error {
	host := strings.TrimSpace(s.cfg.Host)
	from := strings.TrimSpace(s.cfg.From)
	if host == "" || from == "" {
		return fmt.Errorf("smtp sender is not configured")
	}
	if strings.ContainsAny(subject+s.cfg.FromName, "\r\n") {
		return fmt.Errorf("smtp header contains a newline")
	}
	fromMailbox, err := mail.ParseAddress(from)
	if err != nil || strings.TrimSpace(fromMailbox.Address) == "" {
		return fmt.Errorf("smtp from address is invalid")
	}
	toMailbox, err := mail.ParseAddress(to)
	if err != nil || strings.TrimSpace(toMailbox.Address) == "" {
		return fmt.Errorf("smtp recipient address is invalid")
	}
	if strings.TrimSpace(s.cfg.Username) != "" && strings.TrimSpace(s.cfg.Password) == "" || strings.TrimSpace(s.cfg.Username) == "" && strings.TrimSpace(s.cfg.Password) != "" {
		return fmt.Errorf("smtp username and password must be configured together")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	address := fmt.Sprintf("%s:%d", host, s.cfg.Port)
	dialer := net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("dial smtp server: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer client.Close()
	if s.cfg.UseTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("smtp server does not support starttls")
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("start smtp tls: %w", err)
		}
	}
	username := strings.TrimSpace(s.cfg.Username)
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, s.cfg.Password, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(fromMailbox.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(toMailbox.Address); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	message := buildMessage(from, to, s.cfg.FromName, subject, plainBody, htmlBody)
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write smtp message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish smtp message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close smtp session: %w", err)
	}
	return nil
}

func buildMessage(from, to, fromName, subject, plainBody, htmlBody string) string {
	boundary := "kc-recharge-email-boundary"
	return strings.Join([]string{
		"From: " + formatEmailAddress(fromName, from),
		"To: " + formatEmailAddress("", to),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: " + messageID(from),
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Auto-Submitted: auto-generated",
		"X-Auto-Response-Suppress: All",
		`Content-Type: multipart/alternative; boundary="` + boundary + `"`,
		"",
		"--" + boundary,
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		plainBody,
		"",
		"--" + boundary,
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		htmlBody,
		"",
		"--" + boundary + "--",
	}, "\r\n")
}

func formatEmailAddress(name, address string) string {
	address = strings.TrimSpace(address)
	if strings.TrimSpace(name) == "" {
		return address
	}
	return (&mail.Address{Name: strings.TrimSpace(name), Address: address}).String()
}

func messageID(from string) string {
	domain := "kc-recharge.local"
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		domain = strings.TrimSpace(from[at+1:])
	}
	return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), domain)
}
