package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ==========================================
// 📂 GROUP DATABASE INITIALIZATION
// ==========================================
func initGroupDB() {
	createTableQuery := `
	CREATE TABLE IF NOT EXISTS group_settings (
		group_jid TEXT PRIMARY KEY,
		antilink BOOLEAN DEFAULT 0,
		antipic BOOLEAN DEFAULT 0,
		antivideo BOOLEAN DEFAULT 0,
		antisticker BOOLEAN DEFAULT 0,
		welcome BOOLEAN DEFAULT 0,
		antidelete BOOLEAN DEFAULT 0
	);`
	settingsDB.Exec(createTableQuery) 
}

// وارننگز کا ریکارڈ رکھنے کے لیے میموری اور تھریڈ سیفٹی لاک
// وارننگز کا ریکارڈ رکھنے کے لیے میموری اور تھریڈ سیفٹی لاک
// وارننگز کا ریکارڈ رکھنے کے لیے میموری اور لاک
var linkWarnings = make(map[string]int)
var warningMutex sync.Mutex

func checkAntiLink(client *whatsmeow.Client, v *events.Message, body string) bool {
	if !v.Info.IsGroup || v.Info.IsFromMe || isGroupAdmin(client, v) { return false }

	if strings.Contains(body, "http://") || strings.Contains(body, "https://") || 
	   strings.Contains(body, "wa.me/") || strings.Contains(body, "chat.whatsapp.com/") {
		   
		var isAntiLinkOn bool
		settingsDB.QueryRow("SELECT antilink FROM group_settings WHERE group_jid = ?", v.Info.Chat.User).Scan(&isAntiLinkOn)
		
		if isAntiLinkOn {
			// 🚀 ڈائریکٹ ایکشن (Delete)
			revokeMsg := client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID)
			_, err := client.SendMessage(context.Background(), v.Info.Chat, revokeMsg)
			if err != nil { return false }

			// یوزر ڈیٹا
			senderJID := v.Info.Sender.ToNonAD().String()
			senderNum := v.Info.Sender.ToNonAD().User
			userKey := v.Info.Chat.User + "|" + senderNum
			
			warningMutex.Lock()
			linkWarnings[userKey]++
			strikes := linkWarnings[userKey]
			warningMutex.Unlock()

			if strikes == 1 {
				// ⚠️ First Strike: Warning with proper Mention
				warnText := fmt.Sprintf("🚫 *𝗔𝗡𝗧𝗜-𝗟𝗜𝗡𝗞 𝗦𝗬𝗦𝗧𝗘𝗠*\n\n⚠️ @%s, this is your first and last warning!\nSharing links is strictly prohibited. You will be kicked next time.", senderNum)
				replyMessages(client, v, warnText, []string{senderJID})
			} else {
				// 🚨 Second Strike: Kick
				_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{v.Info.Sender.ToNonAD()}, whatsmeow.ParticipantChangeRemove)
				if err == nil {
					kickText := fmt.Sprintf("🚫 *𝗔𝗡𝗧𝗜-𝗟𝗜𝗡𝗞 𝗦𝗬𝗦𝗧𝗘𝗠*\n\n🚨 @%s has been removed for violating rules!", senderNum)
					replyMessages(client, v, kickText, []string{senderJID})
				}
				
				warningMutex.Lock()
				delete(linkWarnings, userKey)
				warningMutex.Unlock()
			}
			return true 
		}
	}
	return false
}



// ⚙️ Group Settings Toggle
func handleGroupToggle(client *whatsmeow.Client, v *events.Message, settingName string, dbColumn string, args string) {
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "on" && args != "off" {
		replyMessage(client, v, fmt.Sprintf("❌ Invalid usage! Use: `.%s on` or `.%s off`", dbColumn, dbColumn))
		return
	}

	state := false
	if args == "on" { state = true }

	settingsDB.Exec("INSERT OR IGNORE INTO group_settings (group_jid) VALUES (?)", v.Info.Chat.User)
	
	query := fmt.Sprintf("UPDATE group_settings SET %s = ? WHERE group_jid = ?", dbColumn)
	settingsDB.Exec(query, state, v.Info.Chat.User)
	
	react(client, v, "✅")
	replyMessage(client, v, fmt.Sprintf("✅ *%s* is now turned *%s* for this group.", settingName, strings.ToUpper(args)))
}

// ==========================================
// 🛡️ DIRECT ACTION COMMANDS 
// ==========================================

// 🎯 ہدف والا یوزر نکالنے کا ہیلپر
func getTargetJID(v *events.Message, args string) (types.JID, bool) {
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg != nil && extMsg.ContextInfo != nil && extMsg.ContextInfo.Participant != nil {
		target, _ := types.ParseJID(*extMsg.ContextInfo.Participant)
		return target, true
	}
	
	if extMsg != nil && extMsg.ContextInfo != nil && len(extMsg.ContextInfo.MentionedJID) > 0 {
		target, _ := types.ParseJID(extMsg.ContextInfo.MentionedJID[0])
		return target, true
	}

	if args != "" {
		phone := cleanPhoneNumber(args)
		target := types.NewJID(phone, types.DefaultUserServer)
		return target, true
	}

	return types.EmptyJID, false
}

// 🧹 کلین فون نمبر
func cleanPhoneNumber(phone string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' { return r }
		return -1
	}, phone)
	return cleaned
}

// 🚫 Kick
func handleKick(client *whatsmeow.Client, v *events.Message, args string) {
	targetJID, ok := getTargetJID(v, args)
	if !ok {
		replyMessage(client, v, "❌ Please reply to a message, tag someone, or provide a number to kick.")
		return
	}

	_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangeRemove)
	if err != nil {
		replyMessage(client, v, "❌ Action Failed! I am probably not an Admin.")
		return
	}
	react(client, v, "✅")
}

// ➕ Add (With Privacy Check Fix)
func handleAdd(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ Please provide a phone number to add.\nExample: `.add 923001234567`")
		return
	}

	targetJID := types.NewJID(cleanPhoneNumber(args), types.DefaultUserServer)
	
	resp, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangeAdd)
	if err != nil {
		replyMessage(client, v, "❌ Action Failed! I am probably not an Admin.")
		return
	}

	// 🛠️ FIX: Status کی جگہ Error یوز کیا گیا ہے
	for _, change := range resp {
		if change.JID.User == targetJID.User {
			if change.Error == 403 {
				replyMessage(client, v, "❌ Failed! The user has strict Privacy Settings. They cannot be added directly.")
				return
			}
		}
	}
	
	react(client, v, "✅")
	replyMessage(client, v, "✅ User added successfully!")
}

// 🔼 Promote
func handlePromote(client *whatsmeow.Client, v *events.Message, args string) {
	targetJID, ok := getTargetJID(v, args)
	if !ok { replyMessage(client, v, "❌ Target not found."); return }

	_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangePromote)
	if err != nil { 
		replyMessage(client, v, "❌ Action Failed! I am probably not an Admin.") 
	} else { 
		react(client, v, "✅") // 🛠️ FIX: React Arguments
	}
}

// 🔽 Demote
func handleDemote(client *whatsmeow.Client, v *events.Message, args string) {
	targetJID, ok := getTargetJID(v, args)
	if !ok { replyMessage(client, v, "❌ Target not found."); return }

	_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangeDemote)
	if err != nil { 
		replyMessage(client, v, "❌ Action Failed! I am probably not an Admin.") 
	} else { 
		react(client, v, "✅") // 🛠️ FIX: React Arguments
	}
}

// 🔓 Group Open/Close
func handleGroupState(client *whatsmeow.Client, v *events.Message, state string) {
	isClosed := false
	if state == "close" { isClosed = true } else if state != "open" {
		replyMessage(client, v, "❌ Invalid usage! Use `.group open` or `.group close`")
		return
	}
	
	err := client.SetGroupAnnounce(context.Background(), v.Info.Chat, isClosed)
	if err != nil { 
		replyMessage(client, v, "❌ Action Failed! I am probably not an Admin.") 
	} else { 
		react(client, v, "✅") // 🛠️ FIX: React Arguments
	}
}

// 🗑️ Delete Message
func handleDel(client *whatsmeow.Client, v *events.Message) {
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.StanzaID == nil {
		replyMessage(client, v, "❌ Please reply to a message to delete it!")
		return
	}

	targetID := *extMsg.ContextInfo.StanzaID

	// 🛠️ FIX: RevokeMessage arguments updated
	_, err := client.RevokeMessage(context.Background(), v.Info.Chat, types.MessageID(targetID))
	if err != nil {
		replyMessage(client, v, "❌ Failed to delete. I might not be an Admin, or the message is too old.")
	}
}

// 📢 Tag All & Ghost Tag
func handleTags(client *whatsmeow.Client, v *events.Message, isHidden bool, args string) {
	groupInfo, err := client.GetGroupInfo(context.Background(), v.Info.Chat)
	if err != nil { return }

	var mentions []string
	var textBuilder strings.Builder

	if !isHidden {
		textBuilder.WriteString("📢 *TAGGING EVERYONE*\n\n")
		if args != "" { textBuilder.WriteString(fmt.Sprintf("💬 *Message:* %s\n\n", args)) }
	} else {
		textBuilder.WriteString(args)
	}

	for _, p := range groupInfo.Participants {
		mentions = append(mentions, p.JID.String())
		if !isHidden { textBuilder.WriteString(fmt.Sprintf("❖ @%s\n", p.JID.User)) }
	}

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(textBuilder.String()),
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: mentions,
			},
		},
	})
}

// ==========================================
// 🚀 .vv COMMAND (Silent Media Extractor)
// ==========================================
func handleVV(client *whatsmeow.Client, v *events.Message) {
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		// ❌ Koi reply bahar nahi — silent fail
		return
	}

	quoted := extMsg.ContextInfo.QuotedMessage
	var data []byte
	var err error
	var msg waProto.Message

	// Bot ka apna DM — sirf yahan jaayega sab
	botSelf := client.Store.ID.ToNonAD()
	selfJID := types.NewJID(botSelf.User, types.DefaultUserServer)

	extractMedia := func(m *waProto.Message) bool {
		if img := m.GetImageMessage(); img != nil {
			data, err = client.Download(context.Background(), img)
			if err == nil {
				up, _ := client.Upload(context.Background(), data, whatsmeow.MediaImage)
				msg.ImageMessage = &waProto.ImageMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("image/jpeg"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))),
				}
				return true
			}
		} else if vid := m.GetVideoMessage(); vid != nil {
			data, err = client.Download(context.Background(), vid)
			if err == nil {
				up, _ := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
				msg.VideoMessage = &waProto.VideoMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("video/mp4"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))),
				}
				return true
			}
		} else if aud := m.GetAudioMessage(); aud != nil {
			data, err = client.Download(context.Background(), aud)
			if err == nil {
				up, _ := client.Upload(context.Background(), data, whatsmeow.MediaAudio)
				msg.AudioMessage = &waProto.AudioMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("audio/ogg; codecs=opus"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))), PTT: proto.Bool(true),
				}
				return true
			}
		}
		return false
	}

	if vo := quoted.GetViewOnceMessage(); vo != nil {
		extractMedia(vo.GetMessage())
	} else if vo2 := quoted.GetViewOnceMessageV2(); vo2 != nil {
		extractMedia(vo2.GetMessage())
	} else if vo3 := quoted.GetViewOnceMessageV2Extension(); vo3 != nil {
		extractMedia(vo3.GetMessage())
	} else {
		extractMedia(quoted)
	}

	if data == nil {
		// ❌ Silent fail — bahar koi message nahi
		return
	}

	// Sender info
	cleanSender := strings.Split(v.Info.Chat.User, "@")[0]
	chatType := "👤 Private DM"
	if v.Info.IsGroup {
		chatType = fmt.Sprintf("👥 Group: %s", v.Info.Chat.ToNonAD().User)
	}

	// 1. Info card sirf apne DM mein
	infoText := fmt.Sprintf(`🕵️ *VV Extracted!*
👤 *From:* @%s
📌 *Chat:* %s

_No one knows_ 🤫`, cleanSender, chatType)

	client.SendMessage(context.Background(), selfJID, &waProto.Message{
		Conversation: proto.String(infoText),
	})

	// 2. Media sirf apne DM mein — koi react/reply bahar nahi
	client.SendMessage(context.Background(), selfJID, &msg)
	// ❌ Koi react nahi, koi reply nahi bahar
}

// ==========================================
// 👑 GROUP ADMIN CHECKER FUNCTION
// ==========================================
func isGroupAdmin(client *whatsmeow.Client, v *events.Message) bool {
	if !strings.Contains(v.Info.Chat.String(), "@g.us") {
		return false
	}

	groupInfo, err := client.GetGroupInfo(context.Background(), v.Info.Chat)
	if err != nil {
		return false
	}

	senderNum := v.Info.Sender.ToNonAD().User

	for _, participant := range groupInfo.Participants {
		if participant.JID.User == senderNum && (participant.IsAdmin || participant.IsSuperAdmin) {
			return true 
		}
	}

	return false
}
