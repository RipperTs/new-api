package common

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/smtp"
	"regexp"
	"strings"
	"time"
)

func generateMessageID() (string, error) {
	split := strings.Split(SMTPFrom, "@")
	if len(split) < 2 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := strings.Split(SMTPFrom, "@")[1]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

// normalizeContentForCache 标准化内容用于缓存，移除动态变化的ID信息
func normalizeContentForCache(content string) string {
	// 移除常见的动态ID模式
	patterns := []string{
		`ID:\s*[a-zA-Z0-9-]+`,                                          // ID: xxx
		`id:\s*[a-zA-Z0-9-]+`,                                          // id: xxx
		`请求ID[：:]\s*[a-zA-Z0-9-]+`,                                     // 请求ID: xxx
		`request[_\s]*id[：:]\s*[a-zA-Z0-9-]+`,                          // request_id: xxx
		`[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}`, // UUID格式
		`[a-f0-9]{32}`,                                                 // 32位MD5/哈希
		`[a-zA-Z0-9]{20,}`,                                             // 长字符串ID（20+字符）
	}

	normalized := content
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + pattern)
		normalized = re.ReplaceAllString(normalized, "[ID]")
	}

	return normalized
}

func SendEmail(subject string, receiver string, content string) error {
	return sendEmail(subject, SplitEmailRecipients(receiver), nil, content, false)
}

func SendEmailWithCc(subject string, receiver string, cc string, content string) error {
	return sendEmail(subject, SplitEmailRecipients(receiver), SplitEmailRecipients(cc), content, false)
}

func SendEmailWithCcNoCache(subject string, receiver string, cc string, content string) error {
	return sendEmail(subject, SplitEmailRecipients(receiver), SplitEmailRecipients(cc), content, true)
}

func SplitEmailRecipients(receivers string) []string {
	normalized := strings.NewReplacer(",", ";", "\n", ";", "\r", ";").Replace(receivers)
	items := strings.Split(normalized, ";")
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		email := strings.TrimSpace(item)
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		result = append(result, email)
	}
	return result
}

func JoinEmailRecipients(receivers []string) string {
	return strings.Join(receivers, ";")
}

func sendEmail(subject string, to []string, cc []string, content string, skipCache bool) error {
	headerRecipients := func(receivers []string) string {
		return strings.Join(receivers, ", ")
	}

	cacheMD5Key := ""
	if SMTPFrom == "" { // for compatibility
		SMTPFrom = SMTPAccount
	}
	id, err2 := generateMessageID()
	if err2 != nil {
		return err2
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return fmt.Errorf("SMTP 服务器未配置")
	}
	if len(to) == 0 {
		return fmt.Errorf("收件人不能为空")
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	headerBuilder := strings.Builder{}
	headerBuilder.WriteString(fmt.Sprintf("To: %s\r\n", headerRecipients(to)))
	if len(cc) > 0 {
		headerBuilder.WriteString(fmt.Sprintf("Cc: %s\r\n", headerRecipients(cc)))
	}
	headerBuilder.WriteString(fmt.Sprintf("From: %s<%s>\r\n", SystemName, SMTPFrom))
	headerBuilder.WriteString(fmt.Sprintf("Subject: %s\r\n", encodedSubject))
	headerBuilder.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	headerBuilder.WriteString(fmt.Sprintf("Message-ID: %s\r\n", id))
	headerBuilder.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	headerBuilder.WriteString(content)
	headerBuilder.WriteString("\r\n")
	mail := []byte(headerBuilder.String())

	if !skipCache {
		normalizedContent := normalizeContentForCache(content)
		contentLen := len(normalizedContent)
		if contentLen > 100 {
			contentLen = 100
		}
		truncatedContent := normalizedContent[:contentLen]
		hash := md5.Sum([]byte(truncatedContent))
		cacheMD5Key = "email_cache:" + hex.EncodeToString(hash[:])
		redisValue, _ := RedisGet(cacheMD5Key)
		if redisValue != "" {
			return nil
		}
	}

	auth := smtp.PlainAuth("", SMTPAccount, SMTPToken, SMTPServer)
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	allRecipients := append(append([]string{}, to...), cc...)
	var err error
	if SMTPPort == 465 || SMTPSSLEnabled {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         SMTPServer,
		}
		conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", SMTPServer, SMTPPort), tlsConfig)
		if err != nil {
			return err
		}
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			return err
		}
		defer client.Close()
		if err = client.Auth(auth); err != nil {
			return err
		}
		if err = client.Mail(SMTPFrom); err != nil {
			return err
		}
		for _, receiver := range allRecipients {
			if err = client.Rcpt(receiver); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write(mail)
		if err != nil {
			return err
		}
		err = w.Close()
		if err != nil {
			return err
		}
	} else if isOutlookServer(SMTPAccount) || SMTPServer == "smtp.azurecomm.net" {
		auth = LoginAuth(SMTPAccount, SMTPToken)
		err = smtp.SendMail(addr, auth, SMTPFrom, allRecipients, mail)
	} else {
		err = smtp.SendMail(addr, auth, SMTPFrom, allRecipients, mail)
	}
	if err == nil && cacheMD5Key != "" {
		_ = RedisSet(cacheMD5Key, "1", time.Duration(GetEnvOrDefault("INTERVAL_TIME", 60))*time.Second)
	}
	return err
}
