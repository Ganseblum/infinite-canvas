// Package mail 发送注册验证、密码重置等站点邮件：smtp 驱动走真实 SMTP，
// log 驱动把整封邮件打进日志（本地联调与测试取令牌的来源）。
package mail

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
)

const (
	// dialTimeout 限制 TCP 建连，sessionTimeout 限制整封邮件的收发时长，
	// 避免 SMTP 黑洞时 goroutine 与连接无限堆积。
	dialTimeout    = 10 * time.Second
	sessionTimeout = 60 * time.Second
	// queueTimeout 限制在发送槽位上的排队时长；满载超过该时长直接放弃并记日志。
	queueTimeout = 30 * time.Second
	maxInFlight  = 8
)

// Config 是邮件驱动的配置项，取值来自 config 包的同名环境变量。
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

// Mailer 按 cfg.Driver 投递邮件；sem 是发送槽位，限制同时打开的 SMTP 连接数。
type Mailer struct {
	cfg Config
	sem chan struct{}
}

func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg, sem: make(chan struct{}, maxInFlight)}
}

// Message 是一封待发邮件，Body 为 UTF-8 纯文本。
type Message struct {
	To      string
	Subject string
	Body    string
}

// Send 立即返回、异步投递：排队超时或发送失败只记日志，不向调用方反馈结果。
func (m *Mailer) Send(msg Message) {
	if m.cfg.Driver == "log" {
		slog.Info("邮件(日志驱动)", "to", msg.To, "subject", msg.Subject, "body", msg.Body)
		return
	}
	// 异步发送，失败只记日志不阻塞主流程
	go func() {
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
		case <-time.After(queueTimeout):
			slog.Error("邮件发送排队超时，放弃发送", "to", msg.To)
			return
		}
		if err := m.sendSMTP(msg); err != nil {
			slog.Error("邮件发送失败", "to", msg.To, "err", err)
		}
	}()
}

// sendSMTP 全程复用同一条连接完成认证与发信。
// 此前 starttls 分支先在一条连接上完成 STARTTLS+AUTH，
// 却用 smtp.SendMail(addr, nil, …) 另开未认证连接发送，导致要求认证的
// SMTP 服务器拒收注册验证与找回密码邮件；两条路径统一后不再有该缺陷。
func (m *Mailer) sendSMTP(msg Message) error {
	addr := fmt.Sprintf("%s:%s", m.cfg.Host, m.cfg.Port)
	a := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	header := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"From: " + m.cfg.From + "\r\n" +
		"To: " + msg.To + "\r\n" +
		"Subject: " + msg.Subject + "\r\n\r\n"
	body := header + msg.Body

	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(sessionTimeout))

	if m.cfg.Security == "ssl" {
		conn = tls.Client(conn, &tls.Config{ServerName: m.cfg.Host})
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()

	if m.cfg.Security != "ssl" {
		// starttls 是显式要求：服务器不支持就报错，不做明文回退。
		if err := client.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
			return err
		}
	}
	if err := client.Auth(a); err != nil {
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
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
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
		Body:    "请点击以下链接重置密码（1 小时内有效）：\n\n" + link,
	}
}
