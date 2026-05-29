package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ==========================================
// 🛠️ DATABASE INIT
// ==========================================
func initPersonalLogDB() {
	query := `CREATE TABLE IF NOT EXISTS personal_log_settings (
		bot_jid TEXT PRIMARY KEY,
		anti_delete_enabled INTEGER DEFAULT 0,
		anti_vv_enabled INTEGER DEFAULT 0,
		anti_vv_trigger TEXT DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS message_cache (
		msg_id TEXT PRIMARY KEY,
		sender_jid TEXT,
		msg_content BLOB,
		timestamp INTEGER
	);`
	settingsDB.Exec(query)

	// Safe migration — naye columns add karo agar na hon
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_delete_enabled INTEGER DEFAULT 0")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_vv_enabled INTEGER DEFAULT 0")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_vv_trigger TEXT DEFAULT ''")
	// Purane columns bhi rakhte hain compatibility ke liye
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_delete_group TEXT DEFAULT ''")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_vv_group TEXT DEFAULT ''")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_edit_group TEXT DEFAULT ''")
}

// ==========================================
// 💾 MESSAGE CACHE SAVER
// ==========================================
func handleAntiDeleteSave(client *whatsmeow.Client, v *events.Message) {
	if v.Message == nil || v.Info.IsFromMe {
		return
	}

	botJID := client.Store.ID.ToNonAD().User

	// Check karo koi bhi anti feature on hai?
	var adEnabled, avvEnabled int
	var aeGroup string
	err := settingsDB.QueryRow(
		"SELECT anti_delete_enabled, anti_vv_enabled, anti_edit_group FROM personal_log_settings WHERE bot_jid = ?",
		botJID,
	).Scan(&adEnabled, &avvEnabled, &aeGroup)

	if err != nil || (adEnabled == 0 && avvEnabled == 0 && aeGroup == "") {
		return
	}

	msgBytes, protoErr := proto.Marshal(v.Message)
	if protoErr == nil {
		settingsDB.Exec(
			"INSERT OR REPLACE INTO message_cache (msg_id, sender_jid, msg_content, timestamp) VALUES (?, ?, ?, ?)",
			v.Info.ID, v.Info.Sender.String(), msgBytes, v.Info.Timestamp.Unix(),
		)
	}
}

// ==========================================
// 🛡️ ANTI-DELETE TOGGLE
// ✅ Ab sirf bot ke apne DM mein ayega
// ==========================================
func handleAntiDeleteToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "on" && args != "off" {
		replyMessage(client, v, "❌ Use: `.antidelete on` or `.antidelete off`")
		return
	}

	botJID := client.Store.ID.ToNonAD().User
	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	if args == "on" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_delete_enabled = 1 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Anti-Delete ON!*\n🔒 Deleted messages will come *only to your own DM* (bot number). No one else will see them!")
	} else {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_delete_enabled = 0 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-Delete OFF!*")
	}
}

// ==========================================
// 🛡️ ANTI-VV TOGGLE
// ✅ Ab sirf bot ke apne DM mein ayega
// ==========================================
func handleAntiVVToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()

	args = strings.TrimSpace(args)
	parts := strings.Fields(args)

	if len(parts) == 0 {
		replyMessage(client, v, "❌ Use: `.antivv on`, `.antivv off`, or `.antivv set <word>`")
		return
	}

	botJID := client.Store.ID.ToNonAD().User
	cmd := strings.ToLower(parts[0])

	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	if cmd == "on" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_enabled = 1 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Anti-VV ON!*\n🔒 View-once media will come *only to your own DM* (bot number). Sender ko kuch pata nahi chalega!")

	} else if cmd == "off" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_enabled = 0 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-VV OFF!*")

	} else if cmd == "set" {
		if len(parts) < 2 {
			replyMessage(client, v, "❌ *Error:* Please provide a trigger word.\nExample: `.antivv set nice`")
			return
		}
		triggerWord := strings.ToLower(parts[1])
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_trigger = ? WHERE bot_jid = ?", triggerWord, botJID)
		react(client, v, "✅")
		replyMessage(client, v, fmt.Sprintf("🕵️ *Trigger Set!*\nReply to any view-once with *\"%s\"* to secretly get it in your DM.", triggerWord))

	} else {
		replyMessage(client, v, "❌ Invalid command. Use `on`, `off`, or `set <word>`")
	}
}

// ==========================================
// 🚫 ANTI-DELETE REVOKE HANDLER
// ✅ Sirf bot ke apne DM mein forward hoga
// ==========================================
func handleAntiDeleteRevoke(client *whatsmeow.Client, v *events.Message) {
	if v.Info.IsFromMe {
		return
	}

	botJID := client.Store.ID.ToNonAD().User

	// Check: Anti-Delete on hai?
	var adEnabled int
	err := settingsDB.QueryRow(
		"SELECT anti_delete_enabled FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&adEnabled)
	if err != nil || adEnabled == 0 {
		return
	}

	deletedMsgID := v.Message.GetProtocolMessage().GetKey().GetID()
	senderJID := v.Info.Sender.ToNonAD().User

	// Cache se original message nikalo
	var rawMsg []byte
	var msgTimestamp int64
	err = settingsDB.QueryRow(
		"SELECT msg_content, timestamp FROM message_cache WHERE msg_id = ?", deletedMsgID,
	).Scan(&rawMsg, &msgTimestamp)
	if err != nil {
		return
	}

	var originalMsg waProto.Message
	proto.Unmarshal(rawMsg, &originalMsg)

	// ✅ TARGET = Bot ka apna DM (khud apne aap ko message)
	botSelfJID, _ := types.ParseJID(botJID + "@s.whatsapp.net")
	botFullJID := client.Store.ID.ToNonAD().String()

	loc, _ := time.LoadLocation("Asia/Karachi")
	sentTime := time.Unix(msgTimestamp, 0).In(loc).Format("02 Jan 2006, 03:04 PM")
	deletedTime := time.Now().In(loc).Format("02 Jan 2006, 03:04 PM")
	cleanSender := strings.Split(senderJID, "@")[0]

	chatContext := "👤 *Type:* Private Chat (DM)"
	if v.Info.IsGroup {
		chatContext = fmt.Sprintf("👥 *Group:* %s", v.Info.Chat.ToNonAD().String())
	}

	warningText := fmt.Sprintf(`❖ ── ✦ 🚫 𝗔𝗡𝗧𝗜-𝗗𝗘𝗟𝗘𝗧𝗘 𝗔𝗟𝗘𝗥𝗧 🚫 ✦ ── ❖

👤 *Sender:* @%s
%s
📅 *Sent At:* %s
🗑️ *Deleted At:* %s

_Attempted to delete this message!_
╰──────────────────────╯`, cleanSender, chatContext, sentTime, deletedTime)

	// 1️⃣ Original message apne DM mein
	resp, sendErr := client.SendMessage(context.Background(), botSelfJID, &originalMsg)

	// 2️⃣ Uske neeche alert card
	if sendErr == nil {
		replyMsg := &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(warningText),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:      proto.String(resp.ID),
					Participant:   proto.String(botFullJID),
					QuotedMessage: &originalMsg,
					MentionedJID:  []string{},
				},
			},
		}
		client.SendMessage(context.Background(), botSelfJID, replyMsg)
	}
}

// ==========================================
// 🕵️ STEALTH VV EXTRACTOR (Trigger Word)
// ✅ Sirf bot ke apne DM mein forward hoga
// ==========================================
func handleStealthVVTrigger(client *whatsmeow.Client, v *events.Message) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ [STEALTH CRASH PREVENTED]: %v\n", r)
		}
	}()

	if settingsDB == nil {
		return
	}
	if client == nil || client.Store == nil || client.Store.ID == nil {
		return
	}

	botJID := client.Store.ID.ToNonAD().User

	var avvEnabled int
	var triggerWord string
	err := settingsDB.QueryRow(
		"SELECT anti_vv_enabled, anti_vv_trigger FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&avvEnabled, &triggerWord)

	if err != nil || avvEnabled == 0 || triggerWord == "" {
		return
	}

	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil {
		return
	}

	msgText := strings.ToLower(strings.TrimSpace(extMsg.GetText()))
	if msgText != triggerWord {
		return
	}

	if extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		return
	}

	quoted := extMsg.ContextInfo.QuotedMessage
	var data []byte
	var extractErr error
	var finalMsg waProto.Message
	var mType whatsmeow.MediaType

	extractMedia := func(m *waProto.Message) bool {
		if img := m.GetImageMessage(); img != nil {
			data, extractErr = client.Download(context.Background(), img)
			mType = whatsmeow.MediaImage
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, mType)
				finalMsg.ImageMessage = &waProto.ImageMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("image/jpeg"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))),
				}
				return true
			}
		} else if vid := m.GetVideoMessage(); vid != nil {
			data, extractErr = client.Download(context.Background(), vid)
			mType = whatsmeow.MediaVideo
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, mType)
				finalMsg.VideoMessage = &waProto.VideoMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("video/mp4"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))),
				}
				return true
			}
		} else if aud := m.GetAudioMessage(); aud != nil {
			data, extractErr = client.Download(context.Background(), aud)
			mType = whatsmeow.MediaAudio
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, mType)
				finalMsg.AudioMessage = &waProto.AudioMessage{
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

	if data != nil && len(data) > 0 {
		// ✅ TARGET = Bot ka apna DM
		botSelfJID, _ := types.ParseJID(botJID + "@s.whatsapp.net")
		botFullJID := client.Store.ID.ToNonAD().String()
		cleanSender := strings.Split(v.Info.Chat.User, "@")[0]

		resp, sendErr := client.SendMessage(context.Background(), botSelfJID, &finalMsg)

		if sendErr == nil {
			caption := fmt.Sprintf(`❖ ── ✦ 🕵️ 𝗩𝗩 𝗘𝗫𝗧𝗥𝗔𝗖𝗧 ✦ ── ❖

👤 *From Chat:* @%s
🔑 *Trigger:* "%s"
🔒 _Sirf aap dekh sakte hain!_
╰──────────────────────╯`, cleanSender, triggerWord)

			replyMsg := &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(caption),
					ContextInfo: &waProto.ContextInfo{
						StanzaID:      proto.String(resp.ID),
						Participant:   proto.String(botFullJID),
						QuotedMessage: &finalMsg,
						MentionedJID:  []string{},
					},
				},
			}
			client.SendMessage(context.Background(), botSelfJID, replyMsg)
		}
	}
}

// ==========================================
// ✏️ ANTI-EDIT TOGGLE
// ==========================================
func handleAntiEditToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()
	if !v.Info.IsGroup {
		replyMessage(client, v, "❌ *Error:* Please use this command inside your intended 'Log Group'.")
		return
	}
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "on" && args != "off" {
		replyMessage(client, v, "❌ Use: `.antiedit on` or `.antiedit off`")
		return
	}

	botJID := client.Store.ID.ToNonAD().User
	chatJID := v.Info.Chat.ToNonAD().String()

	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	if args == "on" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_edit_group = ? WHERE bot_jid = ?", chatJID, botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Anti-Edit Activated!*\nEdited messages will be forwarded to this group.")
	} else {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_edit_group = '' WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-Edit Deactivated!*")
	}
}

// ==========================================
// ✏️ ANTI-EDIT HANDLER
// ==========================================
func handleAntiEdit(client *whatsmeow.Client, v *events.Message) {
	if v.Info.IsFromMe {
		return
	}

	protocolMsg := v.Message.GetProtocolMessage()
	if protocolMsg == nil || protocolMsg.GetType() != waProto.ProtocolMessage_MESSAGE_EDIT {
		return
	}

	botJID := client.Store.ID.ToNonAD().User
	botFullJID := client.Store.ID.ToNonAD().String()

	var logGroup string
	err := settingsDB.QueryRow(
		"SELECT anti_edit_group FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&logGroup)
	if err != nil || logGroup == "" {
		return
	}

	targetJID, _ := types.ParseJID(logGroup)
	originalMsgID := protocolMsg.GetKey().GetID()
	senderJID := v.Info.Sender.ToNonAD().User

	editedMsg := protocolMsg.GetEditedMessage()
	newText := ""
	if editedMsg != nil {
		if editedMsg.GetConversation() != "" {
			newText = editedMsg.GetConversation()
		} else if editedMsg.GetExtendedTextMessage() != nil {
			newText = editedMsg.GetExtendedTextMessage().GetText()
		}
	}

	var rawMsg []byte
	var msgTimestamp int64
	err = settingsDB.QueryRow(
		"SELECT msg_content, timestamp FROM message_cache WHERE msg_id = ?", originalMsgID,
	).Scan(&rawMsg, &msgTimestamp)
	if err != nil {
		return
	}

	var originalMsg waProto.Message
	proto.Unmarshal(rawMsg, &originalMsg)

	loc, _ := time.LoadLocation("Asia/Karachi")
	sentTime := time.Unix(msgTimestamp, 0).In(loc).Format("02 Jan 2006, 03:04 PM")
	editedTime := time.Now().In(loc).Format("02 Jan 2006, 03:04 PM")
	cleanSender := strings.Split(senderJID, "@")[0]

	chatContext := "👤 *Type:* Private Chat (DM)"
	if v.Info.IsGroup {
		chatContext = fmt.Sprintf("👥 *Group JID:* %s", v.Info.Chat.ToNonAD().String())
	}

	warningText := fmt.Sprintf(`❖ ── ✦ ✏️ 𝗔𝗡𝗧𝗜-𝗘𝗗𝗜𝗧 𝗔𝗟𝗘𝗥𝗧 ✏️ ✦ ── ❖

👤 *Sender:* @%s
%s
📅 *Sent At:* %s
✏️ *Edited At:* %s

📝 *New Edited Text:*
_%s_
╰──────────────────────╯`, cleanSender, chatContext, sentTime, editedTime, newText)

	resp, sendErr := client.SendMessage(context.Background(), targetJID, &originalMsg)
	if sendErr == nil {
		replyMsg := &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(warningText),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:      proto.String(resp.ID),
					Participant:   proto.String(botFullJID),
					QuotedMessage: &originalMsg,
					MentionedJID:  []string{},
				},
			},
		}
		client.SendMessage(context.Background(), targetJID, replyMsg)
	}
}
