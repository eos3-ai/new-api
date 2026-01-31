package service

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		now := time.Now().Format("2006-01-02 15:04:05")
		subject := fmt.Sprintf("【通道告警】- %s (#%d)", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf(
			"**【通道告警】- New API 通道监控 🚨**\n"+
				"**📡 通道名称:** %s\n"+
				"**🆔 通道ID:** #%d\n"+
				"**🔄 状态变更: 启用 → 自动禁用**\n"+
				"**🕘 禁用时间:** %s\n"+
				"**⚠️ 告警等级: 严重**\n"+
				"**📝 失败原因:** %s\n"+
				"&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;—— 🧑‍🤝‍🧑 LaiYe科技 -- 运维团队 ——",
			channelError.ChannelName,
			channelError.ChannelId,
			now,
			reason,
		)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
	}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		now := time.Now().Format("2006-01-02 15:04:05")
		subject := fmt.Sprintf("【通道恢复】- %s (#%d)", channelName, channelId)
		content := fmt.Sprintf(
			"**【通道恢复】- New API 通道监控 ✅**\n"+
				"**📡 通道名称:** %s\n"+
				"**🆔 通道ID:** #%d\n"+
				"**🔄 状态变更: 自动禁用 → 启用**\n"+
				"**🕘 恢复时间:** %s\n"+
				"**✨ 状态: 通道已恢复正常**\n"+
				"&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;—— 🧑‍🤝‍🧑 LaiYe科技 -- 运维团队 ——",
			channelName,
			channelId,
			now,
		)
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
}

func ShouldDisableChannel(channelType int, err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if err.StatusCode == http.StatusUnauthorized {
		return true
	}
	if err.StatusCode == http.StatusForbidden {
		switch channelType {
		case constant.ChannelTypeGemini:
			return true
		}
	}
	oaiErr := err.ToOpenAIError()
	switch oaiErr.Code {
	case "invalid_api_key":
		return true
	case "account_deactivated":
		return true
	case "billing_not_active":
		return true
	case "pre_consume_token_quota_failed":
		return true
	case "Arrearage":
		return true
	}
	switch oaiErr.Type {
	case "insufficient_quota":
		return true
	case "insufficient_user_quota":
		return true
	// https://docs.anthropic.com/claude/reference/errors
	case "authentication_error":
		return true
	case "permission_error":
		return true
	case "forbidden":
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
