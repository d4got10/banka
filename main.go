package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const MAX_MESSAGE_BUFFER_SIZE = 2048
const MAX_MESSAGE_BUFFER_COUNT = 1024 * 1024
const MAX_MESSAGE_COUNT_PER_BANKA = 2

type Buffer struct {
	data       []byte
	dataLength int
}

type PooledBuffer struct {
	buffer    *Buffer
	poolIndex int
}

type Packet struct {
	author       *User
	pooledBuffer *PooledBuffer
}

type BufferPool struct {
	freeBuffers chan int
	buffers     []*PooledBuffer
}

func newBufferPool() BufferPool {
	freeBuffers := make(chan int, MAX_MESSAGE_BUFFER_COUNT)
	buffers := make([]*PooledBuffer, MAX_MESSAGE_BUFFER_COUNT)
	for i := 0; i < MAX_MESSAGE_BUFFER_COUNT; i++ {
		buffer := make([]byte, MAX_MESSAGE_BUFFER_SIZE)
		buffers[i] = &PooledBuffer{
			buffer: &Buffer{
				data:       buffer,
				dataLength: 0,
			},
			poolIndex: i,
		}
		freeBuffers <- i
	}

	return BufferPool{
		freeBuffers: freeBuffers,
		buffers:     buffers,
	}
}

func (pool *BufferPool) getBuffer() *PooledBuffer {
	index := <-pool.freeBuffers
	return pool.buffers[index]
}

func (pool *BufferPool) returnBuffer(buffer *PooledBuffer) {
	pool.freeBuffers <- buffer.poolIndex
}

type Banka struct {
	id             int
	buffer         []byte
	messageOffsets []int
	messageCount   int
	mutex          sync.Mutex
	isSealed       bool
	sealDate       time.Time
}

var bankaIsSealedError = errors.New("banka is already sealed")
var bankaIsFullError = errors.New("banka is already full")

func (banka *Banka) isFull() bool {
	return banka.messageCount == MAX_MESSAGE_COUNT_PER_BANKA
}

func (banka *Banka) putMessage(buffer Buffer) error {
	banka.mutex.Lock()
	defer banka.mutex.Unlock()

	if banka.isSealed {
		return bankaIsSealedError
	}

	if banka.isFull() {
		return bankaIsFullError
	}

	// TODO: переиспользовать буфферы для банок.
	banka.buffer = append(banka.buffer, buffer.data[:buffer.dataLength]...)

	offset := banka.messageOffsets[banka.messageCount]
	offset += buffer.dataLength

	banka.messageCount++
	banka.messageOffsets[banka.messageCount] = offset

	return nil
}

func newBanka(id int) *Banka {
	return &Banka{
		id:             id,
		messageOffsets: make([]int, MAX_MESSAGE_COUNT_PER_BANKA+1),
	}
}

type Pogreb struct {
	current *Banka
	mutex   sync.RWMutex
	sealed  []*Banka
}

func newPogreb() *Pogreb {
	return &Pogreb{
		current: newBanka(0),
		mutex:   sync.RWMutex{},
		sealed:  make([]*Banka, 0),
	}
}

func (pogreb *Pogreb) putMessage(buffer Buffer) error {
	for {
		pogreb.mutex.RLock()
		banka := pogreb.current
		err := banka.putMessage(buffer)
		pogreb.mutex.RUnlock()

		// Если банка заполнена, то закрываем и снова пытаемся положить уже в новую баночку
		if errors.Is(err, bankaIsFullError) {
			pogreb.mutex.Lock()
			if pogreb.current.isFull() {
				pogreb.sealCurrentBanka()
			}
			pogreb.mutex.Unlock()
		} else {
			return err
		}
	}
}

func (pogreb *Pogreb) getCurrentBanka() *Banka {
	pogreb.mutex.RLock()
	defer pogreb.mutex.RUnlock()
	return pogreb.current
}

func (pogreb *Pogreb) sealCurrentBanka() {
	current := pogreb.current
	current.isSealed = true
	current.sealDate = time.Now()
	// TODO: подумать на фичей хранения запечатанных банок.
	pogreb.sealed = append(pogreb.sealed, current)
	pogreb.current = newBanka(len(pogreb.sealed))
}

func processBanka(banka *Banka) error {
	if !banka.isSealed {
		return errors.New("can't process Banka that is not sealed")
	}

	fmt.Printf("Открываем банку с id %d\n", banka.id)
	for i := 0; i < banka.messageCount; i++ {
		messageStart := banka.messageOffsets[i]
		messageEnd := banka.messageOffsets[i+1]
		messageBuffer := banka.buffer[messageStart:messageEnd]
		message := string(messageBuffer)

		fmt.Printf("[%d]: \"%s\"\n", i, message)
	}

	return nil
}

const (
	Uninitialized = iota
	WaitingForMessage
	ReceivingMessage
	SendingMessage
	Closed
)

type User struct {
	id           int
	conn         net.Conn
	state        int
	buffer       []byte
	writtenBytes int
}

func NewUser(conn net.Conn, id int) *User {
	return &User{
		id:     id,
		state:  ReceivingMessage,
		conn:   conn,
		buffer: make([]byte, MAX_MESSAGE_BUFFER_SIZE),
	}
}

func handlePacket(author *User, packet Packet) error {
	if author.state == Closed {
		return nil
	}

	return nil
}

func main() {
	messageChannel := make(chan Packet, MAX_MESSAGE_BUFFER_COUNT)
	bufferPool := newBufferPool()

	pogreb := newPogreb()
	processedBankaCount := 0

	go func() {
		for packet := range messageChannel {
			pooledBuffer := packet.pooledBuffer
			// fmt.Printf("Получено сообщение: %s\n", string(Buffer))

			err := pogreb.putMessage(*pooledBuffer.buffer)
			if err != nil {
				fmt.Printf("Ошибка при записи в погреб: %s\n", err.Error())
			}

			fmt.Printf("Количество банок в погребе: %d\n", len(pogreb.sealed))
			for processedBankaCount < len(pogreb.sealed) {
				err := processBanka(pogreb.sealed[processedBankaCount])
				if err != nil {
					fmt.Printf("Ошибка при обработке банки [%d]: %s\n", processedBankaCount, err.Error())
				}
				processedBankaCount++
			}

			bufferPool.returnBuffer(pooledBuffer)
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
		go handleConnection(conn, messageChannel, bufferPool, cnt)
	}
}

func handleConnection(conn net.Conn, messageChannel chan Packet, bufferPool BufferPool, id int) {
	defer conn.Close()

	user := NewUser(conn, id)

	for {
		pooledBuffer := bufferPool.getBuffer()
		buffer := pooledBuffer.buffer

		n, err := conn.Read(buffer.data)
		buffer.dataLength = n

		if n == 0 || err == io.EOF {
			fmt.Printf("%s отключился\n", conn.RemoteAddr())
			bufferPool.returnBuffer(pooledBuffer)
			break
		}

		if err != nil {
			fmt.Println("Ошибка чтения данных:", err)
			bufferPool.returnBuffer(pooledBuffer)
			break
		}

		messageChannel <- Packet{
			pooledBuffer: pooledBuffer,
			author:       user,
		}
	}
}
