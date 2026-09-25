package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	// Start the background sweeper to instantly kill idle/expired chats
	go Sweeper()

	http.HandleFunc("/api/create", handleCreateChat)
	http.HandleFunc("/ws/", handleWebSocket)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Cheetah Chat backend running on :%s (Zero-Allocation Mode)\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Println("Server error:", err)
	}
}

// POST /api/create
// Creates a new chat if the 100 global limit is not reached
func handleCreateChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == "OPTIONS" {
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, err := CreateChat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"chat_id": id})
}

// GET /ws/{chat_id}?user={user_id}
// Upgrades HTTP to our custom WebSocket and attaches user to the Static Grid
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 1. Extract parameters
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.Error(w, "Missing chat ID", http.StatusBadRequest)
		return
	}
	chatID := parts[2]
	userID := r.URL.Query().Get("user")
	if userID == "" {
		http.Error(w, "Missing user query param", http.StatusBadRequest)
		return
	}

	// 2. Locate the chat in the global memory grid
	chatIndex, err := FindChat(chatID)
	if err != nil {
		http.Error(w, "Chat not found or expired", http.StatusNotFound)
		return
	}
	chat := &GlobalChatGrid[chatIndex]

	// 3. Upgrade HTTP -> TCP WebSocket using our custom library-free code
	conn, err := Upgrade(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	// 4. Register the user slot in the static grid
	chat.Mutex.Lock()
	userSlotIndex := -1
	for i := 0; i < MaxUsersPerChat; i++ {
		if !chat.Users[i].IsActive {
			chat.Users[i].IsActive = true
			chat.Users[i].UserID = userID
			chat.Users[i].Conn = conn
			userSlotIndex = i
			break
		}
	}
	chat.Mutex.Unlock()

	// If all 10 slots are full, reject
	if userSlotIndex == -1 {
		conn.WriteMessage([]byte("ERROR: Chat is full (Max 10 users)"))
		return
	}

	// Cleanup on disconnect
	defer func() {
		chat.Mutex.Lock()
		chat.Users[userSlotIndex].IsActive = false
		chat.Users[userSlotIndex].Conn = nil
		chat.Mutex.Unlock()
		
		// If 0 users left, this will mark the chat slot as IsActive = false
		CheckEmptyChat(chatIndex) 
	}()

	// 5. Send historical messages back to the newly connected user
	chat.Mutex.Lock()
	for i := 0; i < chat.MessageCount; i++ {
		msgData := chat.Messages[i].Data[:chat.Messages[i].Length]
		conn.WriteMessage(msgData)
	}
	chat.Mutex.Unlock()

	// 6. Wait for new messages
	for {
		payload, err := conn.ReadMessage()
		if err != nil {
			if err != io.EOF {
				fmt.Println("Read error:", err)
			}
			break // Disconnect
		}

		// Enforce strict 255 character limit directly in memory
		if len(payload) > MaxMessageLength {
			payload = payload[:MaxMessageLength] // Truncate
		}

		chat.Mutex.Lock()
		
		// Enforce 10,000 message lock limit
		if chat.MessageCount >= MaxMessages {
			conn.WriteMessage([]byte("ERROR: Chat locked (Max 10000 messages reached)"))
			chat.Mutex.Unlock()
			continue
		}

		// Save directly into the static message slot array
		msgIndex := chat.MessageCount
		chat.Messages[msgIndex].Length = len(payload)
		copy(chat.Messages[msgIndex].Data[:], payload)
		chat.MessageCount++
		chat.LastActivityAt = time.Now().Unix()

		// Broadcast message to all active user slots
		for i := 0; i < MaxUsersPerChat; i++ {
			if chat.Users[i].IsActive && chat.Users[i].Conn != nil {
				chat.Users[i].Conn.WriteMessage(payload)
			}
		}
		chat.Mutex.Unlock()
	}
}
