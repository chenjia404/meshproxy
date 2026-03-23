package chat

import (
	"strings"
)

const lastMessagePreviewRunes = 20

func truncateUTF8Runes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}

// messagePreviewFromMessage 產生會話列表用的預覽字串（最多 lastMessagePreviewRunes 個 Unicode 字元）。
func messagePreviewFromMessage(msg Message) string {
	switch msg.MsgType {
	case MessageTypeChatFile:
		fn := strings.TrimSpace(msg.FileName)
		if fn != "" {
			return truncateUTF8Runes(fn, lastMessagePreviewRunes)
		}
		return truncateUTF8Runes("[檔案]", lastMessagePreviewRunes)
	case MessageTypeGroupInviteNote:
		t := strings.TrimSpace(msg.Plaintext)
		if t == "" {
			return truncateUTF8Runes("[群組邀請]", lastMessagePreviewRunes)
		}
		return truncateUTF8Runes(t, lastMessagePreviewRunes)
	default:
		return truncateUTF8Runes(msg.Plaintext, lastMessagePreviewRunes)
	}
}
