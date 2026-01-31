package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// WebhookPayload webhook 通知的负载数据
type WebhookPayload struct {
	Type      string        `json:"type"`
	Title     string        `json:"title"`
	Content   string        `json:"content"`
	Values    []interface{} `json:"values,omitempty"`
	Timestamp int64         `json:"timestamp"`
}

// DingTalkMarkdownPayload 钉钉 Markdown 消息格式
type DingTalkMarkdownPayload struct {
	MsgType  string `json:"msgtype"`
	Markdown struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	} `json:"markdown"`
}

// DingTalkTextPayload 钉钉文本消息格式
type DingTalkTextPayload struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

// generateSignature 生成 webhook 签名
func generateSignature(secret string, payload []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// isDingTalkWebhook 检测是否为钉钉 webhook
func isDingTalkWebhook(webhookURL string) bool {
	return strings.Contains(webhookURL, "oapi.dingtalk.com") || strings.Contains(webhookURL, "api.dingtalk.com")
}

// generateDingTalkSign 生成钉钉加签
func generateDingTalkSign(secret string, timestamp int64) string {
	stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// SendWebhookNotify 发送 webhook 通知
func SendWebhookNotify(webhookURL string, secret string, data dto.Notify) error {
	// 处理占位符
	content := data.Content
	for _, value := range data.Values {
		content = fmt.Sprintf(content, value)
	}

	var payloadBytes []byte
	var err error
	var finalWebhookURL string
	timestamp := time.Now().Unix()

	// 检测是否为钉钉 webhook
	if isDingTalkWebhook(webhookURL) {
		// 使用钉钉 Markdown 格式
		payload := DingTalkMarkdownPayload{
			MsgType: "markdown",
		}
		payload.Markdown.Title = data.Title
		// 钉钉 Markdown 里单个换行不会生效，转成强制换行
		dingTalkContent := strings.ReplaceAll(content, "\r\n", "\n")
		dingTalkContent = strings.ReplaceAll(dingTalkContent, "\n", "  \n")
		includeTitle := data.Title != ""
		if strings.HasPrefix(data.Type, dto.NotifyTypeChannelUpdate) {
			includeTitle = false
		}
		if includeTitle {
			payload.Markdown.Text = fmt.Sprintf("### %s\n\n%s", data.Title, dingTalkContent)
		} else {
			payload.Markdown.Text = dingTalkContent
		}

		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal dingtalk webhook payload: %v", err)
		}

		// 如果有 secret，添加钉钉加签
		finalWebhookURL = webhookURL
		if secret != "" {
			timestampMs := time.Now().UnixNano() / 1e6
			sign := generateDingTalkSign(secret, timestampMs)

			// 将 timestamp 和 sign 添加到 URL 参数
			if strings.Contains(webhookURL, "?") {
				finalWebhookURL = fmt.Sprintf("%s&timestamp=%d&sign=%s", webhookURL, timestampMs, url.QueryEscape(sign))
			} else {
				finalWebhookURL = fmt.Sprintf("%s?timestamp=%d&sign=%s", webhookURL, timestampMs, url.QueryEscape(sign))
			}
		}
	} else {
		// 使用通用 webhook 格式
		payload := WebhookPayload{
			Type:      data.Type,
			Title:     data.Title,
			Content:   content,
			Values:    data.Values,
			Timestamp: timestamp,
		}

		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal webhook payload: %v", err)
		}
		finalWebhookURL = webhookURL
	}

	// 创建 HTTP 请求
	var req *http.Request
	var resp *http.Response

	if system_setting.EnableWorker() {
		// 构建worker请求数据
		workerReq := &WorkerRequest{
			URL:    finalWebhookURL,
			Key:    system_setting.WorkerValidKey,
			Method: http.MethodPost,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: payloadBytes,
		}

		// 如果有secret且不是钉钉webhook，添加签名到headers
		if secret != "" && !isDingTalkWebhook(webhookURL) {
			signature := generateSignature(secret, payloadBytes)
			workerReq.Headers["X-Webhook-Signature"] = signature
			workerReq.Headers["Authorization"] = "Bearer " + secret
		}

		resp, err = DoWorkerRequest(workerReq)
		if err != nil {
			return fmt.Errorf("failed to send webhook request through worker: %v", err)
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("webhook request failed with status code: %d", resp.StatusCode)
		}
	} else {
		// SSRF防护：验证Webhook URL（非Worker模式）
		fetchSetting := system_setting.GetFetchSetting()
		if err := common.ValidateURLWithFetchSetting(finalWebhookURL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
			return fmt.Errorf("request reject: %v", err)
		}

		req, err = http.NewRequest(http.MethodPost, finalWebhookURL, bytes.NewBuffer(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to create webhook request: %v", err)
		}

		// 设置请求头
		req.Header.Set("Content-Type", "application/json")

		// 如果有 secret 且不是钉钉webhook，生成签名
		if secret != "" && !isDingTalkWebhook(webhookURL) {
			signature := generateSignature(secret, payloadBytes)
			req.Header.Set("X-Webhook-Signature", signature)
		}

		// 发送请求
		client := GetHttpClient()
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send webhook request: %v", err)
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("webhook request failed with status code: %d", resp.StatusCode)
		}
	}

	return nil
}
