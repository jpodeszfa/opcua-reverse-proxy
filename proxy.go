package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"unicode/utf8"
)

type helPktT struct {
	MessageType       [4]byte
	MessageSize       uint32
	Version           uint32
	ReceiveBufferSize uint32
	SendBufferSize    uint32
	MaxMessageCount   uint32
	MaxChunkCount     uint32
	EndpointSize      uint32
}

func sendError(conn net.Conn, client net.Addr, statusCode uint32, reason string) {
	ln := uint32(len(reason))

	frame := []byte{'E', 'R', 'R', 'F'}
	frame = binary.LittleEndian.AppendUint32(frame, 16+ln)
	frame = binary.LittleEndian.AppendUint32(frame, statusCode)
	frame = binary.LittleEndian.AppendUint32(frame, ln)
	frame = append(frame, []byte(reason)...)

	log.Println("client", client, reason)

	_, err := conn.Write(frame)
	if err != nil {
		log.Println("client", client, "error writing ERR", err)
	}
}

func copier(closer chan struct{}, dst io.Writer, src io.Reader) {
	_, _ = io.Copy(dst, src)
	closer <- struct{}{}
}

func main() {
	configFile, err := os.ReadFile("opcua-reverse-proxy.json")
	if err != nil {
		log.Println(err)
		return
	}

	var config struct {
		Listen    string            `json:"listen"`
		Endpoints map[string]string `json:"endpoints"`
	}
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		log.Println(err)
		return
	}

	signalTrap := make(chan os.Signal, 1)
	signal.Notify(signalTrap, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		signalType := <-signalTrap
		log.Println("exiting, signal", signalType)
		os.Exit(0)
	}()

	server, err := net.Listen("tcp", config.Listen)
	if err != nil {
		panic(err)
	}
	log.Println("listening on", config.Listen)

	for {
		conn, err := server.Accept()
		if err != nil {
			log.Println("error accepting connection:", err)
			continue
		}
		client := conn.RemoteAddr()
		log.Println("client", client, "new connection")

		buf := make([]byte, 32+4096)
		ln, err := conn.Read(buf)
		if err != nil || ln < 32 {
			sendError(conn, client, 0x80050000, "communication error")
			conn.Close()
			continue
		}

		var helloPkt helPktT
		err = binary.Read(bytes.NewReader(buf), binary.LittleEndian, &helloPkt)

		if err != nil ||
			helloPkt.MessageType[0] != 'H' ||
			helloPkt.MessageType[1] != 'E' ||
			helloPkt.MessageType[2] != 'L' ||
			helloPkt.MessageType[3] != 'F' ||
			helloPkt.Version != 0 ||
			helloPkt.MessageSize > 32+4096 ||
			helloPkt.EndpointSize > 4096 ||
			helloPkt.EndpointSize != helloPkt.MessageSize-32 ||
			helloPkt.MessageSize != uint32(ln) ||
			!utf8.Valid(buf[32:helloPkt.MessageSize]) {
			sendError(conn, client, 0x80070000, "decoding error")
			conn.Close()
			continue
		}

		endpoint := string(buf[32:helloPkt.MessageSize])
		remote, ok := config.Endpoints[endpoint]

		if !ok {
			sendError(conn, client, 0x80830000, "endpoint "+endpoint+" not configured")
			conn.Close()
			continue
		}

		log.Println("client", client, "endpoint", endpoint)

		go func() {
			defer conn.Close()

			conn2, err := net.Dial("tcp", remote)
			if err != nil {
				sendError(conn, client, 0x807d0000, "endpoint '"+endpoint+"' not responding")
				return
			}

			defer conn2.Close()
			closer := make(chan struct{}, 2)
			_, err = conn2.Write(buf[:helloPkt.MessageSize])
			if err != nil {
				log.Println("client", client, "error writing HEL", err)
				return
			}
			go copier(closer, conn2, conn)
			go copier(closer, conn, conn2)
			<-closer

			log.Println("client", client, "end connection")
		}()
	}
}
