package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func readErrorTest(t *testing.T, e error) {
	if e != nil {
		t.Fatal(e.Error())
	}
}

func TestWebSocketConnection(t *testing.T) {
	isSuccess := false

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(handleUpgrade))
	defer server.Close()

	// Connect to the server
	url := "ws" + server.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	readErrorTest(t, err)
	defer ws.Close()

	// Read welcome message
	_, message, err := ws.ReadMessage()
	readErrorTest(t, err)
	isSuccess = assert.Contains(t, string(message), "hello from websocket server")
	if !isSuccess {
		t.Fatal("failed wellcome test")
	}
	isSuccess = false

	// Send a message and read the reply
	// send
	sendTextMsg := &PayloadMsg{Msg: "hello"}
	sendTextMsgByte, err := json.Marshal(sendTextMsg)
	readErrorTest(t, err)
	err = ws.WriteMessage(websocket.TextMessage, sendTextMsgByte)
	readErrorTest(t, err)

	// read
	_, message, err = ws.ReadMessage()
	readErrorTest(t, err)
	receiveTextMsg := &PayloadMsg{}
	err = json.Unmarshal(message, receiveTextMsg)
	readErrorTest(t, err)
	isSuccess = assert.Contains(t, sendTextMsg.Msg, receiveTextMsg.Msg)
	if !isSuccess {
		t.Fatal("failed send a message test")
	}
	isSuccess = false

	// Test ping
	err = ws.WriteMessage(websocket.PingMessage, nil)
	readErrorTest(t, err)

	// test read pong -> ws.ReadMessage() cannot read ping/pong/close message https://github.com/gorilla/websocket/issues/744
	// _, message, err = ws.ReadMessage()
	// readErrorTest(t, err)
	// assert.Equal(t, websocket.PongMessage, message)

	// Test server sending pings
	// time.Sleep(2 * pingPeriod)
	// _, message, err = ws.ReadMessage()
	// readErrorTest(t, err)
	// assert.Equal(t, websocket.PingMessage, message)

	// test close connnection
	err = ws.WriteMessage(websocket.CloseMessage, nil)
	readErrorTest(t, err)
}
