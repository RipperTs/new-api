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
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	mail := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s<%s>\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		receiver, SystemName, SMTPFrom, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))

	// 标准化内容并生成 MD5 缓存键，移除动态ID避免缓存失效
	normalizedContent := normalizeContentForCache(content)
	// 使用标准化后的内容前100个字符生成哈希
	contentLen := len(normalizedContent)
	if contentLen > 100 {
		contentLen = 100
	}
	truncatedContent := normalizedContent[:contentLen]
	hash := md5.Sum([]byte(truncatedContent))
	cacheMD5Key := "email_cache:" + hex.EncodeToString(hash[:])
	redisValue, _ := RedisGet(cacheMD5Key)
	if redisValue != "" {
		return nil
	}

	auth := smtp.PlainAuth("", SMTPAccount, SMTPToken, SMTPServer)
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	to := strings.Split(receiver, ";")
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
		receiverEmails := strings.Split(receiver, ";")
		for _, receiver := range receiverEmails {
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
		err = smtp.SendMail(addr, auth, SMTPFrom, to, mail)
	} else {
		err = smtp.SendMail(addr, auth, SMTPFrom, to, mail)
	}
	_ = RedisSet(cacheMD5Key, "1", time.Duration(GetEnvOrDefault("INTERVAL_TIME", 60))*time.Second)
	return err
}
