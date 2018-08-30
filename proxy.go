package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/gorilla/websocket"
	"github.com/kardianos/service"
)

type Config struct {
	ListenPort int    `toml:"port"`
	VNCAddr    string `toml:"vnc"`
}

var (
	config  Config
	slogger service.Logger
)

type program struct{}

func (p *program) Start(s service.Service) error {
	go run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	// TODO
	return nil
}

var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsToTCP(wsConn *websocket.Conn, tcpConn net.Conn) chan error {
	done := make(chan error, 2)
	go func() {
		defer wsConn.Close()
		defer tcpConn.Close()
		for {
			t, m, err := wsConn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			if t == websocket.BinaryMessage {
				_, err = tcpConn.Write(m)
				if err != nil {
					done <- err
					return
				}
			} else {
				log.Println("invalid message", t, m)
			}
		}
		done <- nil
	}()
	return done
}

func tcpToWs(tcpConn net.Conn, wsConn *websocket.Conn) chan error {
	done := make(chan error, 2)
	go func() {
		defer wsConn.Close()
		defer tcpConn.Close()
		data := make([]byte, 4096)
		for {
			l, err := tcpConn.Read(data)
			if err != nil {
				done <- err
				return
			}
			err = wsConn.WriteMessage(websocket.BinaryMessage, data[0:l])
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	return done
}

func handleProxyConnection(w http.ResponseWriter, r *http.Request) {
	slogger.Infof("Connected. remote: %s", r.RemoteAddr)
	conn, err := wsupgrader.Upgrade(w, r, http.Header{"Sec-WebSocket-Protocol": {"binary"}})
	if err != nil {
		slogger.Error(err)
		return
	}
	defer conn.Close()

	conn2, err := net.Dial("tcp", config.VNCAddr)
	if err != nil {
		log.Println("connect tcp", err)
		conn.WriteJSON(map[string]interface{}{"error": "connect failed"})
		return
	}
	defer conn2.Close()

	done1 := tcpToWs(conn2, conn)
	done2 := wsToTCP(conn, conn2)

	// wait
	log.Println("done2", <-done2)
	log.Println("done1", <-done1)
	log.Println("disconnect")
}

func run() {
	http.HandleFunc("/websockify", handleProxyConnection)

	listenAddr := ":" + fmt.Sprint(config.ListenPort)
	slogger.Infof("start server on %s", listenAddr)
	http.ListenAndServe(listenAddr, nil)
}

func main() {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	_, err = toml.DecodeFile(dir+"/config.toml", &config)
	if err != nil {
		log.Fatal(err)
		return
	}

	svcConfig := &service.Config{
		Name:        "Websockify",
		DisplayName: "Websockify service for noVNC.",
		Description: "WebSocket to TCP proxy.",
	}

	s, err := service.New(&program{}, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	slogger, err = s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}

	if len(os.Args) > 1 {
		err = service.Control(s, os.Args[1])
		if err != nil {
			log.Fatal(err)
		}
		return
	} else {
		err = s.Run()
		if err != nil {
			slogger.Error(err)
		}
	}
}
