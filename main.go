package main

import (
	"fmt"
	"io"
	"net"
	"os"
)

const MAX_BUFFER_SIZE = 2048
const MAX_BUFFER_COUNT = 1024 * 1024

type buffer = []byte
type bufferPointer struct {
	index  int
	length int
}

func main() {

	messageChannel := make(chan bufferPointer, MAX_BUFFER_COUNT)
	freeBuffers := make(chan int, MAX_BUFFER_COUNT)
	buffers := make([]buffer, MAX_BUFFER_COUNT)
	for i := 0; i < MAX_BUFFER_COUNT; i++ {
		buffers[i] = make([]byte, MAX_BUFFER_SIZE)
		freeBuffers <- i
	}

	go func() {
		for pointer := range messageChannel {
			buffer := buffers[pointer.index][:pointer.length]
			fmt.Printf("Получено сообщение: %s\n", string(buffer))
		}
	}()

	port := 8080
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Println("Ошибка при создании сокета:", err)
		os.Exit(1)
	}
	defer listener.Close()
	fmt.Printf("Сервер запущен на порту %d...\n", port)

	cnt := 0

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Ошибка при принятии соединения:", err)
			continue
		}
		fmt.Println("Новое соединение:", cnt, conn.RemoteAddr())
		cnt++
		go handleConnection(conn, messageChannel, freeBuffers, buffers)
	}
}

func handleConnection(conn net.Conn, messageChannel chan bufferPointer, freeBuffers chan int, buffers []buffer) {
	defer conn.Close()

	for {
		index := <-freeBuffers
		buffer := buffers[index]

		n, err := conn.Read(buffer)

		if n == 0 || err == io.EOF {
			freeBuffers <- index
			fmt.Printf("%s отключился\n", conn.RemoteAddr())
			break
		}

		if err != nil {
			fmt.Println("Ошибка чтения данных:", err)
			freeBuffers <- index
			break
		}

		messageChannel <- bufferPointer{index, n}
	}
}
