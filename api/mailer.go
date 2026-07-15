package api

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Mailer sends transactional email over SMTP. Port 465 uses implicit TLS;
// any other port dials plain and upgrades with STARTTLS when the server
// offers it.
type Mailer struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

func (m Mailer) Configured() bool {
	return strings.TrimSpace(m.Host) != "" && strings.TrimSpace(m.From) != ""
}

func (m Mailer) Send(to string, subject string, body string) error {
	if !m.Configured() {
		return fmt.Errorf("smtp is not configured")
	}
	to = strings.TrimSpace(to)
	if to == "" || strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("recipient address is invalid")
	}
	port := m.Port
	if port == 0 {
		port = 587
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(m.Host, strconv.Itoa(port))

	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: timeout}
	if port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.Host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: m.Host}); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(m.Username) != "" {
		if err := client.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(envelopeAddress(m.From)); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(buildMessage(m.From, to, subject, body)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func buildMessage(from string, to string, subject string, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// envelopeAddress extracts the bare address from a "Name <addr>" style From.
func envelopeAddress(from string) string {
	if start := strings.LastIndex(from, "<"); start != -1 {
		if end := strings.LastIndex(from, ">"); end > start {
			return from[start+1 : end]
		}
	}
	return strings.TrimSpace(from)
}
