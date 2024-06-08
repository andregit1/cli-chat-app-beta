package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type User struct {
	Username string
	Password string
	Conn     *websocket.Conn
	Room     string
}

type Message struct {
	Type    string `json:"type"`
	Sender  string `json:"sender"`
	Target  string `json:"target,omitempty"`
	Content string `json:"content"`
	Room    string `json:"room,omitempty"`
}

var (
	clients    = make(map[*websocket.Conn]*User)
	users      = make(map[string]*User)
	rooms      = make(map[string][]*User)
	broadcast  = make(chan Message)
	upgrader   = websocket.Upgrader{}
	clientLock sync.Mutex
	userLock   sync.Mutex
	roomLock   sync.Mutex
)

func main() {
	http.HandleFunc("/ws", handleConnections)

	go handleMessages()

	go startCLI()

	log.Println("http server started on :8000")
	err := http.ListenAndServe(":8000", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("error: %v", err)
			clientLock.Lock()
			delete(clients, ws)
			clientLock.Unlock()
			break
		}

		switch msg.Type {
		case "signup":
			handleSignup(ws, msg)
		case "signin":
			handleSignin(ws, msg)
		case "signout":
			handleSignout(ws)
		case "create_room":
			handleCreateRoom(ws, msg)
		case "join_room":
			handleJoinRoom(ws, msg)
		case "leave_room":
			handleLeaveRoom(ws, msg)
		case "broadcast", "dm":
			handleChat(ws, msg)
		}
	}
}

func handleSignup(ws *websocket.Conn, msg Message) {
	userLock.Lock()
	defer userLock.Unlock()

	if _, exists := users[msg.Sender]; exists {
		ws.WriteJSON(Message{Type: "error", Content: "Username already exists"})
		return
	}

	users[msg.Sender] = &User{Username: msg.Sender, Password: msg.Content}
	ws.WriteJSON(Message{Type: "info", Content: "Signup successful"})
}

func handleSignin(ws *websocket.Conn, msg Message) {
	userLock.Lock()
	defer userLock.Unlock()

	user, exists := users[msg.Sender]
	if !exists || user.Password != msg.Content {
		ws.WriteJSON(Message{Type: "error", Content: "Invalid username or password"})
		return
	}

	clientLock.Lock()
	clients[ws] = user
	clientLock.Unlock()

	ws.WriteJSON(Message{Type: "info", Content: "Signin successful"})
}

func handleSignout(ws *websocket.Conn) {
	clientLock.Lock()
	defer clientLock.Unlock()

	delete(clients, ws)
	ws.WriteJSON(Message{Type: "info", Content: "Signout successful"})
}

func handleCreateRoom(ws *websocket.Conn, msg Message) {
	roomLock.Lock()
	defer roomLock.Unlock()

	if _, exists := rooms[msg.Content]; exists {
		ws.WriteJSON(Message{Type: "error", Content: "Room already exists"})
		return
	}

	rooms[msg.Content] = []*User{}
	ws.WriteJSON(Message{Type: "info", Content: "Room created successfully"})
}

func handleJoinRoom(ws *websocket.Conn, msg Message) {
	roomLock.Lock()
	defer roomLock.Unlock()

	room, exists := rooms[msg.Content]
	if !exists {
		ws.WriteJSON(Message{Type: "error", Content: "Room does not exist"})
		return
	}

	user := clients[ws]
	user.Room = msg.Content
	rooms[msg.Content] = append(room, user)

	sendChatHistory(ws, msg.Content)

	ws.WriteJSON(Message{Type: "info", Content: "Joined room successfully"})
}

func handleLeaveRoom(ws *websocket.Conn, msg Message) {
	roomLock.Lock()
	defer roomLock.Unlock()

	user := clients[ws]
	room, exists := rooms[user.Room]
	if !exists {
		ws.WriteJSON(Message{Type: "error", Content: "You are not in a room"})
		return
	}

	for i, u := range room {
		if u.Username == user.Username {
			rooms[user.Room] = append(room[:i], room[i+1:]...)
			break
		}
	}

	user.Room = ""
	ws.WriteJSON(Message{Type: "info", Content: "Left room successfully"})
}

func handleChat(ws *websocket.Conn, msg Message) {
	user := clients[ws]
	if user.Room == "" {
		ws.WriteJSON(Message{Type: "error", Content: "You are not in a room"})
		return
	}

	msg.Room = user.Room

	roomLock.Lock()
	defer roomLock.Unlock()

	for _, u := range rooms[user.Room] {
		if msg.Type == "dm" && u.Username != msg.Target && u.Username != msg.Sender {
			continue
		}
		err := u.Conn.WriteJSON(msg)
		if err != nil {
			log.Printf("error: %v", err)
			u.Conn.Close()
			clientLock.Lock()
			delete(clients, u.Conn)
			clientLock.Unlock()
		}
	}

	saveMessageToFile(msg)
}

func startCLI() {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter message: ")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		msg := Message{
			Type:    "broadcast",
			Sender:  "server",
			Content: text,
		}

		broadcast <- msg
	}
}

func handleMessages() {
	for {
		msg := <-broadcast
		roomLock.Lock()
		for _, user := range rooms[msg.Room] {
			err := user.Conn.WriteJSON(msg)
			if err != nil {
				log.Printf("error: %v", err)
				user.Conn.Close()
				clientLock.Lock()
				delete(clients, user.Conn)
				clientLock.Unlock()
			}
		}
		roomLock.Unlock()
		saveMessageToFile(msg)
	}
}

func saveMessageToFile(msg Message) {
	file, err := os.OpenFile(fmt.Sprintf("chat_history_%s.txt", msg.Room), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("error: %v", err)
		return
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	message := fmt.Sprintf("[%s] %s: %s\n", msg.Room, msg.Sender, msg.Content)
	writer.WriteString(message)
	writer.Flush()
}

func sendChatHistory(ws *websocket.Conn, room string) {
	file, err := os.Open(fmt.Sprintf("chat_history_%s.txt", room))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("error: %v", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		ws.WriteJSON(Message{Type: "history", Content: scanner.Text()})
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error: %v", err)
	}
}
