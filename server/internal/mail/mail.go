package mail

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

type Config struct {
	Driver     string // smtp | log
	Host       string
	Port       string
	Username   string
	Password   string
	From       string
	Security   string // ssl | starttls
	AppBaseURL string
}

type Mailer struct {
	cfg Config
}

func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg}
}

type Message struct {
	To      string
	Subject string
	Body    string
}

func (m *Mailer) Send(msg Message) {
	if m.cfg.Driver == "log" {
		slog.Info("邮件(日志驱动)", "to", msg.To, "subject", msg.Subject, "body", msg.Body)
		return
	}
	// 异步发送，失败只记日志不阻塞主流程
	go func() {
		if err := m.sendSMTP(msg); err != nil {
			slog.Error("邮件发送失败", "to", msg.To, "err", err)
		}
	}()
}

func (m *Mailer) sendSMTP(msg Message) error {
	addr := fmt.Sprintf("%s:%s", m.cfg.Host, m.cfg.Port)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	header := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"From: " + m.cfg.From + "\r\n" +
		"To: " + msg.To + "\r\n" +
		"Subject: " + msg.Subject + "\r\n\r\n"
	body := header + msg.Body

	if m.cfg.Security == "ssl" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.cfg.Host})
		if err != nil {
			return err
		}
		client, err := smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Auth(auth); err != nil {
			return err
		}
		if err := client.Mail(m.cfg.From); err != nil {
			return err
		}
		if err := client.Rcpt(msg.To); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return err
		}
		return w.Close()
	}

	// starttls
	client, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
		return err
	}
	if err := client.Auth(auth); err != nil {
		return err
	}
	return smtp.SendMail(addr, nil, m.cfg.From, []string{msg.To}, []byte(body))
}

// VerifyEmailBody 生成邮箱验证邮件正文。
func (m *Mailer) VerifyEmailBody(to, token string) Message {
	link := strings.TrimRight(m.cfg.AppBaseURL, "/") + "/verify-email?token=" + token
	return Message{
		To:      to,
		Subject: "验证你的无限画布账号",
		Body:    "请点击以下链接验证你的邮箱（24 小时内有效）：\n\n" + link,
	}
}

// ResetPasswordBody 生成密码重置邮件正文。
func (m *Mailer) ResetPasswordBody(to, token string) Message {
	link := strings.TrimRight(m.cfg.AppBaseURL, "/") + "/reset-password?token=" + token
	return Message{
		To:      to,
		Subject: "重置你的无限画布密码",
		Body:    "请点击以下链接重置密码（24 小时内有效）：\n\n" + link,
	}
}
