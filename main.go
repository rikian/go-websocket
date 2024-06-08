package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	port       = ":9090"                                // http port
	magicKey   = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" // ws key. see https://datatracker.ietf.org/doc/html/rfc6455#section-1.3
	pingPeriod = 20 * time.Second                       // periode ping 20 second
)

// to accommodate the message type in websocket
type WebSocketMessage struct {
	OpCode  int // see https://datatracker.ietf.org/doc/html/rfc6455#section-5.3
	Payload []byte
}

// for message delivery format client and server
type PayloadMsg struct {
	From string `json:"from"`
	Msg  string `json:"message"`
	Img  string `json:"img"`
	Id   string `json:"id"`
	Name string `json:"name"`
}

// for profile user
type UserProfile struct {
	Name string `json:"name"`
	Img  string `json:"img"`
}

// to accommodate each user's websocket connection
type UserConn struct {
	ID   string
	Name string
	Img  string
	Conn net.Conn
}

// we need a mutex to avoid race conditions (goroutines accessing the same variable)
var userConn = struct {
	sync.Mutex
	conns map[string]UserConn
}{conns: make(map[string]UserConn)}

// to add user connnection to the pool
func AddUserConn(id, name, imgName string, conn net.Conn) {
	userConn.Lock()
	defer userConn.Unlock()

	userConn.conns[id] = UserConn{
		ID:   id,
		Name: name,
		Img:  imgName,
		Conn: conn,
	}

	fmt.Printf("Added user: %s\n", name)
}

// to delete user connection from the pool
func DeleteUserConn(id string) bool {
	userConn.Lock()
	defer userConn.Unlock()

	if _, exists := userConn.conns[id]; exists {
		delete(userConn.conns, id)
		fmt.Printf("Deleted user: %s\n", id)
		return true
	}
	return false
}

// to send a broadcash message
func Notification(forAll bool, id string, msg *PayloadMsg) {
	if forAll {
		for _, v := range userConn.conns {
			v.Conn.Write(parseToBuffer(msg))
		}
	} else {
		for _, v := range userConn.conns {
			if v.ID == id {
				continue
			} else {
				v.Conn.Write(parseToBuffer(msg))
			}
		}
	}
}

func generateUUID() (string, error) {
	u := make([]byte, 16)
	_, err := rand.Read(u)
	if err != nil {
		return "", err
	}

	u[8] = (u[8] | 0x80) & 0xBF
	u[6] = (u[6] | 0x40) & 0x4F

	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:]), nil
}

func handleUpgrade(w http.ResponseWriter, r *http.Request) {
	// Each connection must have a unique ID
	id, err := generateUUID()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Hijack lets the caller take over the connection. After a call to Hijack the HTTP server library will not do anything else
	// with the connection. It becomes the caller's responsibility to manage and close the connection.
	socket, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, "Error hijacking connection", http.StatusInternalServerError)
		return
	}

	// Opening Handshake from server. See https://datatracker.ietf.org/doc/html/rfc6455#section-4.2.2
	websocketKey := r.Header.Get("Sec-WebSocket-Key") + magicKey
	hash := sha1.New()
	hash.Write([]byte(websocketKey))
	newWebSocketKey := base64.StdEncoding.EncodeToString(hash.Sum(nil))

	responseHeaders := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Protocol: json\r\n" +
		"Sec-WebSocket-Accept: " + newWebSocketKey + "\r\n\r\n"

	// tell the user if handshake success
	socket.Write([]byte(responseHeaders))

	// create notification / broadcast message
	wsMessage := &PayloadMsg{}

	// for ping connection
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	go func() {
		defer socket.Close()
		for {
			select {
			case <-ticker.C:
				// we need to add ping connection per period for stable connection
				ping := createPingMessage()
				_, err := socket.Write(ping)
				if err != nil {
					fmt.Println("Failed to send ping:", err)
					return
				}
			default:
				// parse every incoming message
				msg, err := readMessage(socket)
				if err != nil {
					DeleteUserConn(id)
					return
				}
				// we need to handle the message according to the opCode. See https://datatracker.ietf.org/doc/html/rfc6455#section-5.3
				switch msg.OpCode {
				case 0x1: // Text frame
					fmt.Printf("Received text message: %s\n", msg.Payload)
					err = json.Unmarshal(msg.Payload, wsMessage)
					if err != nil {
						wsMessage.From = "Server"
						wsMessage.Msg = "Unsupported message"
						socket.Write(parseToBuffer(wsMessage))
					} else {
						if wsMessage.From == "auth" {
							userProfile := &UserProfile{}
							_ = json.Unmarshal(msg.Payload, userProfile)

							// add user connection to the pool
							AddUserConn(id, userProfile.Name, userProfile.Img, socket)

							// say hello to the user
							wsMessage.From = "server"
							wsMessage.Id = id
							wsMessage.Img = "img3.png"
							wsMessage.Msg = fmt.Sprintf("hello %v from websocket server", userConn.conns[id].Name)
							socket.Write(parseToBuffer(wsMessage))

							// set online notification
							wsMessage.From = "status"
							wsMessage.Msg = fmt.Sprintf("%v", len(userConn.conns))
							Notification(true, "", wsMessage)

							// broadcast if a user has joined to chat
							wsMessage.From = "server"
							wsMessage.Img = "img3.png"
							wsMessage.Msg = userConn.conns[id].Name + " join the chat"
							Notification(false, id, wsMessage)

							// broadcast add list user
							for _, v := range userConn.conns {
								wsMessage.From = "profile-add"
								wsMessage.Id = v.ID
								wsMessage.Name = v.Name
								wsMessage.Img = v.Img
								wsMessage.Msg = ""
								Notification(true, "", wsMessage)
							}
						} else {
							for _, v := range userConn.conns {
								if v.ID == id {
									continue
								} else {
									v.Conn.Write(parseToBuffer(wsMessage))
								}
							}
						}
					}
				case 0x2: // Binary frame
					fmt.Println("Received binary message")
				case 0x8: // Connection close
					fmt.Println("Received close frame")
					wsMessage.From = "Server"
					wsMessage.Img = "img3.png"
					wsMessage.Msg = userConn.conns[id].Name + " left the chat"
					Notification(false, id, wsMessage)
					DeleteUserConn(id)
					wsMessage.From = "status"
					wsMessage.Msg = fmt.Sprintf("%v", len(userConn.conns))
					Notification(true, "", wsMessage)

					// broadcast delete user from list
					wsMessage.From = "profile-delete"
					wsMessage.Id = id
					wsMessage.Msg = ""
					Notification(true, "", wsMessage)
					return
				case 0x9: // Ping
					fmt.Println("Received ping")
					pong := createPongMessage()
					socket.Write(pong)
				case 0xA: // Pong
					fmt.Println("Received pong")
					// Typically, you don't need to do anything on receiving a pong
				default:
					fmt.Printf("Received unsupported frame: %d\n", msg.OpCode)
				}

				wsMessage = &PayloadMsg{}
			}
		}
	}()
}

// format for decode message https://datatracker.ietf.org/doc/html/rfc6455#section-5.2
func readMessage(socket net.Conn) (*WebSocketMessage, error) {
	var buf [2]byte
	_, err := socket.Read(buf[:2])
	if err != nil {
		return nil, err
	}

	fin := buf[0]&0x80 != 0
	opCode := buf[0] & 0x0F
	masked := buf[1]&0x80 != 0
	payloadLen := int(buf[1] & 0x7F)

	if !fin {
		return nil, fmt.Errorf("fragmented frames not supported")
	}

	if opCode >= 0x3 && opCode <= 0x7 || opCode >= 0xB && opCode <= 0xF {
		return nil, fmt.Errorf("unsupported frame")
	}

	if payloadLen == 126 {
		var extended [2]byte
		_, err := socket.Read(extended[:2])
		if err != nil {
			return nil, err
		}
		payloadLen = int(extended[0])<<8 | int(extended[1])
	} else if payloadLen == 127 {
		// we do not handle big text (>65kb)
		return nil, fmt.Errorf("large frames not supported")
	}

	var maskKey [4]byte
	if masked {
		_, err := socket.Read(maskKey[:4])
		if err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	_, err = socket.Read(payload)
	if err != nil {
		return nil, err
	}

	if masked {
		for i := 0; i < payloadLen; i++ {
			payload[i] ^= maskKey[i%4]
		}
	}

	return &WebSocketMessage{
		OpCode:  int(opCode),
		Payload: payload,
	}, nil
}

// format for encode message https://datatracker.ietf.org/doc/html/rfc6455#section-5.2
func parseToBuffer(jsonObj *PayloadMsg) []byte {
	str, err := json.Marshal(jsonObj)
	if err != nil {
		log.Println(err)
		return parseToBuffer(&PayloadMsg{Msg: "error when parsing a message"})
	}

	strByteLen := len(str)
	var buffer []byte

	if strByteLen < 0x7e {
		buffer = make([]byte, 2+strByteLen)
		buffer[0] = 129 // 129 -> opCode for text message
		buffer[1] = byte(strByteLen)
		copy(buffer[2:], str)
	} else if strByteLen >= 0x7e && strByteLen <= 0xffff {
		buffer = make([]byte, 4+strByteLen)
		buffer[0] = 129 // 129 -> opCode for text message
		buffer[1] = 126 // 126 -> frame to notify the contents of the message approximately 0x7e && 0xffff (65kb)
		buffer[2] = byte(strByteLen >> 8)
		buffer[3] = byte(strByteLen & 0xff)
		copy(buffer[4:], str)
	} else {
		buffer = make([]byte, 10+strByteLen)
		buffer[0] = 129 // 129 -> opCode for text message
		buffer[1] = 127 // 127 -> frame to notify the contents of the message is more than 65kb
		buffer[2] = 0
		buffer[3] = 0
		buffer[4] = 0
		buffer[5] = 0
		buffer[6] = byte(strByteLen >> 24)
		buffer[7] = byte((strByteLen >> 16) & 0xff)
		buffer[8] = byte((strByteLen >> 8) & 0xff)
		buffer[9] = byte(strByteLen & 0xff)
		copy(buffer[10:], str)
	}

	return buffer
}

func createPingMessage() []byte {
	return []byte{0x89, 0x00} // Ping frame with no payload (0x89 -> opCode for ping)
}

func createPongMessage() []byte {
	return []byte{0x8A, 0x00} // Pong frame with no payload (0x8A -> opCode for pong)
}

func main() {
	server := &http.Server{
		Addr: port,
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Read the HTML file
		filePath := "index.html"
		file, err := os.Open(filePath)
		if err != nil {
			http.Error(w, "Could not open requested file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Read the file content
		content, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Could not read requested file", http.StatusInternalServerError)
			return
		}

		// Set the content type to text/html
		w.Header().Set("Content-Type", "text/html")

		// Write the content to the response
		w.Write(content)
	})

	http.HandleFunc("/static/img/", func(w http.ResponseWriter, r *http.Request) {
		filePath := strings.Split(r.URL.Path, "/")
		fileName := filePath[len(filePath)-1]

		file, err := os.Open("./static/img/" + fileName)

		if err != nil {
			http.Error(w, "Could not open requested file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Read the file content
		content, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Could not read requested file", http.StatusInternalServerError)
			return
		}

		// Set the content type to text/html
		w.Header().Set("Content-Type", "image/png")

		// Write the content to the response
		w.Write(content)
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			handleUpgrade(w, r)
		} else {
			http.Error(w, "Not Found", http.StatusNotFound)
		}
	})

	log.Printf("Server listening on port %s", port)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
