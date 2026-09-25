package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Global constraints defined by the architecture
const MaxChats = 100
const MaxUsersPerChat = 10
const MaxMessages = 10000
const MaxMessageLength = 255

// MessageSlot holds a fixed-length byte array for a single message
type MessageSlot struct {
	Length int
	Data   [MaxMessageLength]byte
}

// UserSlot holds an active WebSocket connection
type UserSlot struct {
	IsActive bool
	UserID   string
	Conn     *WSConn
}

// ChatSlot holds the entire static memory for a single chat
type ChatSlot struct {
	IsActive       bool
	ID             string
	CreatedAt      int64
	LastActivityAt int64
	MessageCount   int
	Users          [MaxUsersPerChat]UserSlot
	Messages       [MaxMessages]MessageSlot
	Mutex          sync.Mutex // Protects this specific chat slot from concurrent user actions
}

// GlobalChatGrid is the statically allocated ~260MB chunk of RAM.
// It is allocated exactly once when the program starts.
var GlobalChatGrid [MaxChats]ChatSlot
var gridMutex sync.Mutex // Protects the creation/deletion of chats in the global grid

// generateID creates a random hex string for user/chat IDs
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateChat finds the first inactive slot in the grid and activates it
func CreateChat() (string, error) {
	gridMutex.Lock()
	defer gridMutex.Unlock()

	for i := 0; i < MaxChats; i++ {
		if !GlobalChatGrid[i].IsActive {
			// Found an empty slot! Initialize it.
			GlobalChatGrid[i].IsActive = true
			GlobalChatGrid[i].ID = generateID()

			now := time.Now().Unix()
			GlobalChatGrid[i].CreatedAt = now
			GlobalChatGrid[i].LastActivityAt = now
			GlobalChatGrid[i].MessageCount = 0

			// Clear any leftover users from a previous chat that may have used this memory slot
			for j := 0; j < MaxUsersPerChat; j++ {
				GlobalChatGrid[i].Users[j].IsActive = false
				GlobalChatGrid[i].Users[j].Conn = nil
			}

			return GlobalChatGrid[i].ID, nil
		}
	}
	return "", errors.New("global chat limit reached (100/100)")
}

// FindChat returns the array index of a chat by its ID
func FindChat(id string) (int, error) {
	gridMutex.Lock()
	defer gridMutex.Unlock()

	for i := 0; i < MaxChats; i++ {
		if GlobalChatGrid[i].IsActive && GlobalChatGrid[i].ID == id {
			return i, nil
		}
	}
	return -1, errors.New("chat not found")
}

// CheckEmptyChat checks if 0 users are in the chat. If so, it instantly "deletes" the chat.
func CheckEmptyChat(chatIndex int) {
	chat := &GlobalChatGrid[chatIndex]
	chat.Mutex.Lock()
	defer chat.Mutex.Unlock()

	activeUsers := 0
	for i := 0; i < MaxUsersPerChat; i++ {
		if chat.Users[i].IsActive {
			activeUsers++
		}
	}

	if activeUsers == 0 {
		// No users left, free the slot immediately
		chat.IsActive = false
	}
}

// Sweeper runs in the background and checks for 24h idle or 1 week max lifespan
func Sweeper() {
	for {
		time.Sleep(1 * time.Minute)
		now := time.Now().Unix()

		gridMutex.Lock()
		for i := 0; i < MaxChats; i++ {
			if GlobalChatGrid[i].IsActive {
				// 24 hours = 86400 seconds. 1 week = 604800 seconds
				if now-GlobalChatGrid[i].LastActivityAt > 86400 || now-GlobalChatGrid[i].CreatedAt > 604800 {
					GlobalChatGrid[i].IsActive = false
					kickAllUsers(i)
				}
			}
		}
		gridMutex.Unlock()
	}
}

// kickAllUsers forcibly closes all WebSocket connections in a chat slot
func kickAllUsers(chatIndex int) {
	chat := &GlobalChatGrid[chatIndex]
	chat.Mutex.Lock()
	defer chat.Mutex.Unlock()

	for j := 0; j < MaxUsersPerChat; j++ {
		if chat.Users[j].IsActive && chat.Users[j].Conn != nil {
			chat.Users[j].Conn.Close()
			chat.Users[j].IsActive = false
		}
	}
}
