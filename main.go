package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"strings"
	"google.golang.org/protobuf/proto"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)


// ==========================================
// 🌐 GLOBAL VARIABLES
// ==========================================
var activeClients = make(map[string]*whatsmeow.Client)
var clientsMutex sync.RWMutex
var dbContainer *sqlstore.Container

// ==========================================
// 🔌 WEBSOCKET VARIABLES
// ==========================================
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // یہ Flutter ایپ کو کنیکٹ ہونے کی اجازت دے گا
	},
}
var wsClients = make(map[*websocket.Conn]bool)
var wsMutex sync.Mutex

// ==========================================
// 📂 1. DATABASE INITIALIZATION
// ==========================================
func initDB() {
	dbLog := waLog.Noop

	err := os.MkdirAll("./data", 0755)
	if err != nil {
		log.Fatal("❌ Data directory create error:", err)
	}

	dbContainer, err = sqlstore.New(context.Background(), "sqlite3", "file:./data/sessions.db?_foreign_keys=on", dbLog)
	if err != nil {
		log.Fatal("❌ Database connection error:", err)
	}
	
	log.Println("✅ SQLite Database Initialized Successfully!")
}

// ==========================================
// 🔄 2. AUTO-CONNECT ALL SESSIONS
// ==========================================
func RunAllSessions() {
	devices, err := dbContainer.GetAllDevices(context.Background())
	if err != nil {
		log.Println("❌ Error fetching devices:", err)
		return
	}

	for _, device := range devices {
		clientLog := waLog.Stdout("Client", "ERROR", true)
		client := whatsmeow.NewClient(device, clientLog)

		client.AddEventHandler(func(evt interface{}) {
			EventHandler(client, evt)
		})

		err := client.Connect()
		if err != nil {
			log.Printf("❌ Failed to auto-connect session %s: %v", device.ID.User, err)
			continue
		}

		clientsMutex.Lock()
		activeClients[device.ID.User] = client
		clientsMutex.Unlock()

		log.Printf("🟢 Session %s successfully auto-connected!", device.ID.User)
	}
}

// ==========================================
// 📱 3. PAIRING NEW SESSION
// ==========================================
func ConnectNewSession(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		http.Error(w, "Phone number required", http.StatusBadRequest)
		return
	}

	phone = strings.ReplaceAll(phone, "+", "")
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")

	// 1. نیا ڈیوائس اسٹور تیار کریں
	deviceStore := dbContainer.NewDevice()
	
	// واٹس میو کی آفیشل اور 100% کمپائل ہونے والی ڈیفالٹ سیٹنگز
	store.DeviceProps.Os = proto.String("Linux")
	platformChrome := waCompanionReg.DeviceProps_CHROME
	store.DeviceProps.PlatformType = &platformChrome

	clientLog := waLog.Noop
	client := whatsmeow.NewClient(deviceStore, clientLog)

	client.AddEventHandler(func(evt interface{}) {
		EventHandler(client, evt)
	})

	// 2. واٹس ایپ سرور سے کنکشن قائم کریں
	err := client.Connect()
	if err != nil {
		http.Error(w, "Failed to connect to WhatsApp servers", http.StatusInternalServerError)
		return
	}

	// یہاں بھی آفیشل اور گارنٹیڈ 'PairClientChrome' استعمال کیا گیا ہے
	code, err := client.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		http.Error(w, "Failed to get pairing code", http.StatusInternalServerError)
		client.Disconnect()
		return
	}

	formattedCode := code
	if len(code) == 8 {
		formattedCode = code[:4] + "-" + code[4:]
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "%s", formattedCode)

	log.Printf("🔗 Pairing code [%s] generated via Web for: %s", formattedCode, phone)
}




// ==========================================
// ⚡ 4. WEBSOCKET HANDLERS
// ==========================================
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("⚠️ WebSocket Upgrade Error: %v", err)
		return
	}
	defer ws.Close()

	wsMutex.Lock()
	wsClients[ws] = true
	wsMutex.Unlock()
	
	log.Println("🟢 Flutter App Connected to WebSocket!")

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			log.Println("🔴 Flutter App Disconnected!")
			wsMutex.Lock()
			delete(wsClients, ws)
			wsMutex.Unlock()
			break
		}
	}
}

func BroadcastToWebsocket(payload map[string]interface{}) {
	wsMutex.Lock()
	defer wsMutex.Unlock()

	for client := range wsClients {
		err := client.WriteJSON(payload)
		if err != nil {
			log.Printf("⚠️ WebSocket Send Error: %v", err)
			client.Close()
			delete(wsClients, client)
		}
	}
}

// ==========================================
// 🚀 5. MAIN ENGINE START
// ==========================================
func main() {
	log.Println("🚀 Starting Silent Nexus Engine...")

	// 1. پرانی SQLite ڈیٹا بیس
	initDB()
    initSettingsDB()
	initGroupDB()
	// 2. 💾 ہماری نئی CRM (PostgreSQL) ڈیٹا بیس (save.go میں موجود ہے)
	InitCRMDB() 

	// 3. سیشنز چلائیں
	RunAllSessions()

	// 🌐 CORS Middleware: یہ براؤزر کو ریکویسٹ الاؤ کرے گا
	corsHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*") // سب کو اجازت دیں
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

			// اگر براؤزر صرف کنکشن چیک کر رہا ہے (Preflight request)
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}

	// ⚡ اب اپنے تمام روٹس کو اس Middleware کے اندر لپیٹ دیں
	mux := http.NewServeMux()

	// 4. ویب سرور کی سیٹنگز (یہاں http کی جگہ mux کر دیا گیا ہے)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	mux.HandleFunc("/pair", ConnectNewSession)
	mux.HandleFunc("/ws", HandleWebSocket)

	// ⚡ باقی تمام API روٹس
	mux.HandleFunc("/api/chats", GetChatsAPI)
	mux.HandleFunc("/api/messages", GetMessagesAPI)
	mux.HandleFunc("/api/send", SendMessageAPI)
	mux.HandleFunc("/api/clear_history", ClearBotHistoryAPI)
	
	// ⚡ VIP Admin / Full WhatsApp API Features
	mux.HandleFunc("/api/delete_message", DeleteMessageAPI) // Revoke / Delete For Everyone
	mux.HandleFunc("/api/delete_chat", DeleteChatAPI) // Delete chat / clear messages
	mux.HandleFunc("/api/send_reaction", SendReactionAPI) // React to message
	mux.HandleFunc("/api/block_user", BlockUserAPI) // Block/Unblock
	mux.HandleFunc("/api/send_media", SendMediaAPI) // Send images/video/audio (multipart)
	
	// ⚡ Admin & Auth APIs (VIP Version)
	mux.HandleFunc("/api/auth/login", LoginAPI)
	mux.HandleFunc("/api/admin/keys/create", CreateKeyAPI)
	mux.HandleFunc("/api/admin/keys", GetKeysAPI)
	mux.HandleFunc("/api/admin/keys/delete", DeleteKeyAPI)
	mux.HandleFunc("/api/admin/keys/edit", EditKeyAPI)   // 👈 نیا ایڈٹ روٹ
	mux.HandleFunc("/api/admin/active_bots", GetActiveBotsAPI) // 👈 ایکٹو بوٹس کی لسٹ لانے کا روٹ

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }

	fmt.Printf("🚀 VIP Server running on port %s\n", port)
	
	// 👈 اب http.ListenAndServe میں mux کو corsHandler کے ساتھ چلائیں
	http.ListenAndServe(":"+port, corsHandler(mux))
}
