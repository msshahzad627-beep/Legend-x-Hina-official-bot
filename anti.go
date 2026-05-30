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
	// Main settings table
	settingsDB.Exec(`CREATE TABLE IF NOT EXISTS personal_log_settings (
		bot_jid TEXT PRIMARY KEY,
		anti_delete_enabled INTEGER DEFAULT 0,
		anti_vv_enabled INTEGER DEFAULT 0,
		anti_vv_trigger TEXT DEFAULT '',
		anti_edit_enabled INTEGER DEFAULT 0
	);`)

	// Message cache table
	settingsDB.Exec(`CREATE TABLE IF NOT EXISTS message_cache (
		msg_id TEXT PRIMARY KEY,
		sender_jid TEXT,
		msg_content BLOB,
		timestamp INTEGER
	);`)

	// Safe migrations — agar column pehle se hai to error ignore hoga
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_delete_enabled INTEGER DEFAULT 0")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_vv_enabled INTEGER DEFAULT 0")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_vv_trigger TEXT DEFAULT ''")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_edit_enabled INTEGER DEFAULT 0")
}

// ==========================================
// 🔧 HELPER: Bot ka apna DM JID nikalna
// ==========================================
func getBotSelfJID(client *whatsmeow.Client) types.JID {
	botSelf := client.Store.ID.ToNonAD()
	return types.NewJID(botSelf.User, types.DefaultUserServer)
}

// ==========================================
// 💾 MESSAGE CACHE SAVER
// ==========================================
func handleAntiDeleteSave(client *whatsmeow.Client, v *events.Message) {
	if v.Message == nil || v.Info.IsFromMe { return }

	botJID := client.Store.ID.ToNonAD().User

	// Check karo kya koi bhi feature on hai
	var adEnabled, aeEnabled int
	err := settingsDB.QueryRow(
		"SELECT anti_delete_enabled, anti_edit_enabled FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&adEnabled, &aeEnabled)

	// Agar koi record nahi ya dono off hain — save mat karo
	if err != nil || (adEnabled == 0 && aeEnabled == 0) {
		return
	}

	// Cache mein save karo
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
// Kahi se bhi on karo — sab bot ke DM mein jaayega
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
		replyMessage(client, v, "✅ *Anti-Delete ON!*\nDeleted messages will silently come to your DM. 🤫")
	} else {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_delete_enabled = 0 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-Delete OFF!*")
	}
}

// ==========================================
// 🛡️ ANTI-VV TOGGLE & TRIGGER SETTER
// Kahi se bhi on karo — sab bot ke DM mein jaayega
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
		replyMessage(client, v, "✅ *Anti-VV ON!*\nVV media will silently come to your DM. 🤫\n\n_Set trigger: `.antivv set <word>`_")

	} else if cmd == "off" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_enabled = 0 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-VV OFF!*")

	} else if cmd == "set" {
		if len(parts) < 2 {
			replyMessage(client, v, "❌ Example: `.antivv set nice`")
			return
		}
		triggerWord := strings.ToLower(parts[1])
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_trigger = ? WHERE bot_jid = ?", triggerWord, botJID)
		react(client, v, "✅")
		replyMessage(client, v, fmt.Sprintf("🕵️ *Trigger Set!*\nReply to any VV/media with *\"%s\"* to silently extract it to your DM.", triggerWord))

	} else {
		replyMessage(client, v, "❌ Invalid. Use `on`, `off`, or `set <word>`")
	}
}

// ==========================================
// 🚫 ANTI-DELETE REVOKE HANDLER
// Seedha bot ke DM mein — koi log group nahi
// ==========================================
func handleAntiDeleteRevoke(client *whatsmeow.Client, v *events.Message) {
	botJID := client.Store.ID.ToNonAD().User

	// Check karo anti-delete on hai ya nahi
	var adEnabled int
	err := settingsDB.QueryRow(
		"SELECT anti_delete_enabled FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&adEnabled)
	if err != nil || adEnabled == 0 { return }

	protoMsg := v.Message.GetProtocolMessage()
	if protoMsg == nil { return }

	deletedMsgID := protoMsg.GetKey().GetID()
	
	// Asal sender — protocol key se nikalo (v.Info.Sender nahi)
	var senderJID string
	if protoMsg.GetKey().GetFromMe() {
		// Bot ne khud delete kiya — ignore karo
		return
	}
	// Asal sender cache se nikalo
	var cachedSender string
	var rawMsg []byte
	var msgTimestamp int64
	err = settingsDB.QueryRow(
		"SELECT msg_content, sender_jid, timestamp FROM message_cache WHERE msg_id = ?", deletedMsgID,
	).Scan(&rawMsg, &cachedSender, &msgTimestamp)
	if err != nil { return }

	// Cached sender use karo — jo actual sender tha
	senderJID = strings.Split(cachedSender, "@")[0]
	if senderJID == botJID {
		// Bot ka apna message tha — ignore
		return
	}

	var originalMsg waProto.Message
	proto.Unmarshal(rawMsg, &originalMsg)

	// Bot ka apna DM — target
	targetJID := getBotSelfJID(client)

	loc, _ := time.LoadLocation("Asia/Karachi")
	sentTime := time.Unix(msgTimestamp, 0).In(loc).Format("02 Jan 2006, 03:04 PM")
	deletedTime := time.Now().In(loc).Format("02 Jan 2006, 03:04 PM")
	cleanSender := senderJID

	// Name nikalo — pehle PushName, warna number
	senderName := v.Info.PushName
	if senderName == "" || senderName == "bot" {
		senderName = "+" + cleanSender
	}

	// Deleted message ka content nikalo
	deletedContent := ""
	if txt := originalMsg.GetConversation(); txt != "" {
		deletedContent = "\n💬 *Message:* " + txt
	} else if ext := originalMsg.GetExtendedTextMessage(); ext != nil {
		deletedContent = "\n💬 *Message:* " + ext.GetText()
	} else if originalMsg.GetImageMessage() != nil {
		deletedContent = "\n🖼️ *Type:* Image"
	} else if originalMsg.GetVideoMessage() != nil {
		deletedContent = "\n🎥 *Type:* Video"
	} else if originalMsg.GetAudioMessage() != nil {
		deletedContent = "\n🎵 *Type:* Audio"
	} else if originalMsg.GetStickerMessage() != nil {
		deletedContent = "\n🎭 *Type:* Sticker"
	} else if originalMsg.GetDocumentMessage() != nil {
		deletedContent = "\n📄 *Type:* Document"
	}

	chatContext := "👤 *Type:* Private Chat (DM)"
	if strings.Contains(v.Info.Chat.String(), "status@broadcast") {
		chatContext = "📢 *Type:* WhatsApp Status"
	} else if v.Info.IsGroup {
		groupInfo, gErr := client.GetGroupInfo(context.Background(), v.Info.Chat)
		if gErr == nil && groupInfo.Name != "" {
			chatContext = fmt.Sprintf("👥 *Group:* %s", groupInfo.Name)
		} else {
			chatContext = fmt.Sprintf("👥 *Group:* %s", v.Info.Chat.ToNonAD().User)
		}
	}

	warningText := fmt.Sprintf(`❖ ── ✦ 🚫 𝗔𝗡𝗧𝗜-𝗗𝗘𝗟𝗘𝗧𝗘 𝗔𝗟𝗘𝗥𝗧 🚫 ✦ ── ❖

👤 *Sender:* @%s
👥 *%s*%s
📅 *Sent At:* %s
🗑️ *Deleted At:* %s

_Attempted to delete this message!_
╰──────────────────────╯`, senderName, chatContext, deletedContent, sentTime, deletedTime)

	// 1. Alert card bhejo
	client.SendMessage(context.Background(), targetJID, &waProto.Message{
		Conversation: proto.String(warningText),
	})

	// 2. Deleted content alag se bhejo (fresh — koi forward link nahi)
	sendMediaFresh := func(msg *waProto.Message) {
		if img := msg.GetImageMessage(); img != nil {
			data, err := client.Download(context.Background(), img)
			if err == nil {
				up, err := client.Upload(context.Background(), data, whatsmeow.MediaImage)
				if err == nil {
					client.SendMessage(context.Background(), targetJID, &waProto.Message{
						ImageMessage: &waProto.ImageMessage{
							URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
							MediaKey: up.MediaKey, Mimetype: proto.String("image/jpeg"),
							FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
							FileLength: proto.Uint64(uint64(len(data))),
						},
					})
				}
			}
		} else if vid := msg.GetVideoMessage(); vid != nil {
			data, err := client.Download(context.Background(), vid)
			if err == nil {
				up, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
				if err == nil {
					client.SendMessage(context.Background(), targetJID, &waProto.Message{
						VideoMessage: &waProto.VideoMessage{
							URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
							MediaKey: up.MediaKey, Mimetype: proto.String("video/mp4"),
							FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
							FileLength: proto.Uint64(uint64(len(data))),
						},
					})
				}
			}
		} else if aud := msg.GetAudioMessage(); aud != nil {
			data, err := client.Download(context.Background(), aud)
			if err == nil {
				up, err := client.Upload(context.Background(), data, whatsmeow.MediaAudio)
				if err == nil {
					client.SendMessage(context.Background(), targetJID, &waProto.Message{
						AudioMessage: &waProto.AudioMessage{
							URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
							MediaKey: up.MediaKey, Mimetype: proto.String("audio/ogg; codecs=opus"),
							FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
							FileLength: proto.Uint64(uint64(len(data))), PTT: proto.Bool(true),
						},
					})
				}
			}
		} else if txt := msg.GetConversation(); txt != "" {
			// Sirf text tha — already upar bhej diya alert mein
			_ = txt
		} else if ext := msg.GetExtendedTextMessage(); ext != nil {
			client.SendMessage(context.Background(), targetJID, &waProto.Message{
				Conversation: proto.String(ext.GetText()),
			})
		}
	}
	sendMediaFresh(&originalMsg)
	// ❌ Bahar koi react/reply nahi — bilkul silent
}

// ==========================================
// 🕵️ STEALTH VV EXTRACTOR
// Trigger word pe — sirf bot ke DM mein
// ==========================================
func handleStealthVVTrigger(client *whatsmeow.Client, v *events.Message) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ [VV CRASH PREVENTED]: %v\n", r)
		}
	}()

	if settingsDB == nil { return }
	if client == nil || client.Store == nil || client.Store.ID == nil { return }

	botJID := client.Store.ID.ToNonAD().User

	// Check karo anti-vv on hai aur trigger word set hai
	var vvEnabled int
	var triggerWord string
	err := settingsDB.QueryRow(
		"SELECT anti_vv_enabled, anti_vv_trigger FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&vvEnabled, &triggerWord)

	if err != nil || vvEnabled == 0 || triggerWord == "" { return }

	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil { return }

	msgText := strings.ToLower(strings.TrimSpace(extMsg.GetText()))
	if msgText != triggerWord { return }

	if extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil { return }

	quoted := extMsg.ContextInfo.QuotedMessage
	var data []byte
	var extractErr error
	var finalMsg waProto.Message

	extractMedia := func(m *waProto.Message) bool {
		if img := m.GetImageMessage(); img != nil {
			data, extractErr = client.Download(context.Background(), img)
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, whatsmeow.MediaImage)
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
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
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
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, whatsmeow.MediaAudio)
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
		targetJID := getBotSelfJID(client)

		cleanSender := strings.Split(v.Info.Chat.User, "@")[0]
		chatType := "👤 Private DM"
		if v.Info.IsGroup {
			chatType = fmt.Sprintf("👥 Group: %s", v.Info.Chat.ToNonAD().User)
		}

		// 1. Pehle info card bhejo — koi QuotedMessage nahi (original sender notify na ho)
		caption := fmt.Sprintf(`❖ ── ✦ 🕵️ 𝗩𝗩 𝗘𝗫𝗧𝗥𝗔𝗖𝗧 ✦ ── ❖

👤 *From:* @%s
📌 *Chat:* %s
🔑 *Trigger:* "%s"

_No one knows_ 🤫
╰──────────────────────╯`, cleanSender, chatType, triggerWord)

		client.SendMessage(context.Background(), targetJID, &waProto.Message{
			Conversation: proto.String(caption),
		})

		// 2. Fresh re-upload karke bhejo — koi original sender link nahi
		client.SendMessage(context.Background(), targetJID, &finalMsg)

		// ❌ Bahar koi react/reply nahi — bilkul silent
	}
}

// ==========================================
// ✏️ ANTI-EDIT TOGGLE
// Kahi se bhi on karo — sab bot ke DM mein
// ==========================================
func handleAntiEditToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "on" && args != "off" {
		replyMessage(client, v, "❌ Use: `.antiedit on` or `.antiedit off`")
		return
	}

	botJID := client.Store.ID.ToNonAD().User
	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	if args == "on" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_edit_enabled = 1 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Anti-Edit ON!*\nEdited messages will silently come to your DM. 🤫")
	} else {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_edit_enabled = 0 WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-Edit OFF!*")
	}
}

// ==========================================
// ✏️ ANTI-EDIT HANDLER
// Seedha bot ke DM mein — koi log group nahi
// ==========================================
func handleAntiEdit(client *whatsmeow.Client, v *events.Message) {
	if v.Info.IsFromMe { return }

	protocolMsg := v.Message.GetProtocolMessage()
	if protocolMsg == nil || protocolMsg.GetType() != waProto.ProtocolMessage_MESSAGE_EDIT { return }

	botJID := client.Store.ID.ToNonAD().User

	// Check karo anti-edit on hai
	var aeEnabled int
	err := settingsDB.QueryRow(
		"SELECT anti_edit_enabled FROM personal_log_settings WHERE bot_jid = ?", botJID,
	).Scan(&aeEnabled)
	if err != nil || aeEnabled == 0 { return }

	// Bot ka apna DM
	targetJID := getBotSelfJID(client)

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

	// Cache se original message
	var rawMsg []byte
	var msgTimestamp int64
	err = settingsDB.QueryRow(
		"SELECT msg_content, timestamp FROM message_cache WHERE msg_id = ?", originalMsgID,
	).Scan(&rawMsg, &msgTimestamp)
	if err != nil { return }

	var originalMsg waProto.Message
	proto.Unmarshal(rawMsg, &originalMsg)

	loc, _ := time.LoadLocation("Asia/Karachi")
	sentTime := time.Unix(msgTimestamp, 0).In(loc).Format("02 Jan 2006, 03:04 PM")
	editedTime := time.Now().In(loc).Format("02 Jan 2006, 03:04 PM")
	cleanSender := strings.Split(senderJID, "@")[0]

	chatContext := "👤 *Type:* Private Chat (DM)"
	if v.Info.IsGroup {
		chatContext = fmt.Sprintf("👥 *Group:* %s", v.Info.Chat.ToNonAD().User)
	}

	warningText := fmt.Sprintf(`❖ ── ✦ ✏️ 𝗔𝗡𝗧𝗜-𝗘𝗗𝗜𝗧 𝗔𝗟𝗘𝗥𝗧 ✏️ ✦ ── ❖

👤 *Sender:* @%s
%s
📅 *Sent At:* %s
✏️ *Edited At:* %s

📝 *New Text:*
_%s_

_Silently caught — no one knows_ 🤫
╰──────────────────────╯`, cleanSender, chatContext, sentTime, editedTime, newText)

	// 1. Sirf text alert bhejo — no QuotedMessage (original sender notify na ho)
	client.SendMessage(context.Background(), targetJID, &waProto.Message{
		Conversation: proto.String(warningText),
	})

	// 2. Agar media/text tha to fresh bhejo
	if txt := originalMsg.GetConversation(); txt != "" {
		client.SendMessage(context.Background(), targetJID, &waProto.Message{
			Conversation: proto.String("📝 *Original Message:*\n" + txt),
		})
	} else if ext := originalMsg.GetExtendedTextMessage(); ext != nil {
		client.SendMessage(context.Background(), targetJID, &waProto.Message{
			Conversation: proto.String("📝 *Original Message:*\n" + ext.GetText()),
		})
	}
	// ❌ Bahar koi react/reply nahi
}


