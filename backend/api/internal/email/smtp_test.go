package email

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNewSMTPSenderAppliesReferenceDefaults(t *testing.T) {
	sender := NewSMTPSender(SMTPConfig{Host: "smtp.example.test", From: "noreply@example.test"})
	if sender.cfg.Port != 587 {
		t.Fatalf("port = %d, want 587", sender.cfg.Port)
	}
	if sender.cfg.Timeout != 10*time.Second {
		t.Fatalf("timeout = %s, want 10s", sender.cfg.Timeout)
	}
}

func TestSMTPSenderSendsUTF8MultipartMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	messages := make(chan string, 1)
	serverErrors := make(chan error, 1)
	serverDone := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		if err := writeSMTP(connection, "220 fake smtp ready"); err != nil {
			serverErrors <- err
			return
		}
		if err := expectSMTPCommand(reader, connection, "EHLO"); err != nil {
			serverErrors <- err
			return
		}
		if err := writeSMTP(connection, "250 fake smtp"); err != nil {
			serverErrors <- err
			return
		}
		if err := expectSMTPCommand(reader, connection, "MAIL FROM:"); err != nil {
			serverErrors <- err
			return
		}
		if err := writeSMTP(connection, "250 sender ok"); err != nil {
			serverErrors <- err
			return
		}
		if err := expectSMTPCommand(reader, connection, "RCPT TO:"); err != nil {
			serverErrors <- err
			return
		}
		if err := writeSMTP(connection, "250 recipient ok"); err != nil {
			serverErrors <- err
			return
		}
		if err := expectSMTPCommand(reader, connection, "DATA"); err != nil {
			serverErrors <- err
			return
		}
		if err := writeSMTP(connection, "354 end with dot"); err != nil {
			serverErrors <- err
			return
		}
		var body strings.Builder
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				serverErrors <- readErr
				return
			}
			if line == ".\r\n" {
				break
			}
			body.WriteString(line)
		}
		messages <- body.String()
		if err := writeSMTP(connection, "250 queued"); err != nil {
			serverErrors <- err
			return
		}
		if err := expectSMTPCommand(reader, connection, "QUIT"); err != nil {
			serverErrors <- err
			return
		}
		if err := writeSMTP(connection, "221 bye"); err != nil {
			serverErrors <- err
			return
		}
		close(serverDone)
	}()

	sender := NewSMTPSender(SMTPConfig{
		Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		From: "noreply@example.test", FromName: "KC GPT", Timeout: 2 * time.Second,
	})
	if err := sender.SendGeneric(context.Background(), "buyer@example.test", "购买成功", "plain body", "<p>html body</p>"); err != nil {
		t.Fatalf("SendGeneric() error = %v", err)
	}
	var message string
	select {
	case message = <-messages:
	case serverErr := <-serverErrors:
		if serverErr != nil {
			t.Fatal(serverErr)
		}
		t.Fatal("fake SMTP server finished before receiving the message")
	case <-time.After(2 * time.Second):
		t.Fatal("fake SMTP server did not receive the message")
	}
	if !strings.Contains(message, "plain body") || !strings.Contains(message, "<p>html body</p>") {
		t.Fatalf("multipart body = %q", message)
	}
	if !strings.Contains(message, "multipart/alternative") || !strings.Contains(message, "Subject: =?UTF-8?") {
		t.Fatalf("multipart headers = %q", message)
	}
	select {
	case serverErr := <-serverErrors:
		if serverErr != nil {
			t.Fatal(serverErr)
		}
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fake SMTP server did not finish the SMTP session")
	}
}

func TestSMTPSenderRejectsHeaderInjection(t *testing.T) {
	sender := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", From: "noreply@example.test"})
	if err := sender.SendGeneric(context.Background(), "buyer@example.test", "bad\r\nBcc: attacker@example.test", "body", ""); err == nil {
		t.Fatal("header injection was accepted")
	}
}

func writeSMTP(connection net.Conn, response string) error {
	_, err := connection.Write([]byte(response + "\r\n"))
	return err
}

func expectSMTPCommand(reader *bufio.Reader, connection net.Conn, prefix string) error {
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, prefix) {
		return &unexpectedSMTPCommand{want: prefix, got: line}
	}
	return nil
}

type unexpectedSMTPCommand struct {
	want string
	got  string
}

func (e *unexpectedSMTPCommand) Error() string {
	return "unexpected SMTP command: want prefix " + e.want + ", got " + e.got
}
