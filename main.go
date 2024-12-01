package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const MaxMessageBufferSize = 2048
const MaxMessageBufferCount = 1024 * 1024
const MaxMessageCountPerBanka = 2
const UserSendQueueCapacity = 1024

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

func newBufferPool() *BufferPool {
	freeBuffers := make(chan int, MaxMessageBufferCount)
	buffers := make([]*PooledBuffer, MaxMessageBufferCount)
	for i := 0; i < MaxMessageBufferCount; i++ {
		buffer := make([]byte, MaxMessageBufferSize)
		buffers[i] = &PooledBuffer{
			buffer: &Buffer{
				data:       buffer,
				dataLength: 0,
			},
			poolIndex: i,
		}
		freeBuffers <- i
	}

	return &BufferPool{
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
	return banka.messageCount == MaxMessageCountPerBanka
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
		messageOffsets: make([]int, MaxMessageCountPerBanka+1),
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
	sendQueue    chan *PooledBuffer
}

func NewUser(conn net.Conn, id int) *User {
	return &User{
		id:        id,
		state:     ReceivingMessage,
		conn:      conn,
		buffer:    make([]byte, MaxMessageBufferSize),
		sendQueue: make(chan *PooledBuffer, UserSendQueueCapacity),
	}
}

var noSplitCharacterFoundError = errors.New("no split character found")

func getSplitPosition(message string) (int, error) {
	for i := 0; i < len(message); i++ {
		if message[i] == '|' {
			return i, nil
		}
	}

	return 0, noSplitCharacterFoundError
}

func getKeywords(message string) []string {
	return strings.Fields(message)
}

const (
	PutMessage = iota
)

const (
	PutMessageKeyword = "PUT"
)

func getActionType(keywords []string) (int, error) {
	count := len(keywords)

	if count == 1 && keywords[0] == PutMessageKeyword {
		return PutMessage, nil
	}

	return 0, errors.New("invalid keywords")
}

func handlePacket(packet Packet, pool *BufferPool, pogreb *Pogreb) error {
	author := packet.author
	if author.state == Closed {
		return nil
	}

	pooledBuffer := packet.pooledBuffer
	defer pool.returnBuffer(pooledBuffer)

	buffer := pooledBuffer.buffer
	if !utf8.Valid(buffer.data[:buffer.dataLength]) {
		sendMessage(author, pool, InvalidUtf8Message)
		return errors.New("invalid UTF8")
	}

	message := string(buffer.data[:buffer.dataLength])

	splitPosition, err := getSplitPosition(message)
	if err != nil {
		return err
	}

	payloadBuffer := pool.getBuffer()
	defer pool.returnBuffer(payloadBuffer)

	keywordPart := message[:splitPosition-1]
	payloadPart := message[splitPosition+1:]

	payloadLength := copy(payloadBuffer.buffer.data, payloadPart)
	payloadBuffer.buffer.dataLength = payloadLength

	keywords := getKeywords(keywordPart)
	actionType, err := getActionType(keywords)

	if err != nil {
		return err
	}

	switch actionType {
	case PutMessage:
		err := pogreb.putMessage(*payloadBuffer.buffer)
		if err != nil {
			fmt.Printf("Ошибка при записи в погреб: %s\n", err.Error())
			return err
		}
	}

	sendMessage(author, pool, SuccessMessage)
	return nil
}

const InvalidUtf8Message = "INVALID UTF-8"
const SuccessMessage = "URA"

func sendMessage(target *User, pool *BufferPool, message string) {
	pooledBuffer := pool.getBuffer()
	buffer := pooledBuffer.buffer
	n := copy(buffer.data, message)
	buffer.dataLength = n
	target.sendQueue <- pooledBuffer
}

func main() {
	messageChannel := make(chan Packet, MaxMessageBufferCount)
	bufferPool := newBufferPool()

	pogreb := newPogreb()
	processedBankaCount := 0

	go func() {
		for packet := range messageChannel {
			pooledBuffer := packet.pooledBuffer
			err := handlePacket(packet, bufferPool, pogreb)
			if err != nil {
				fmt.Printf("Не удалось обработать пакет от пользователя [%d]: %s\n", packet.author.id, err.Error())
			}

			// fmt.Printf("Получено сообщение: %s\n", string(Buffer))

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

func (user *User) closeConnection() {
	err := user.conn.Close()
	if err != nil {
		fmt.Printf("Ошибка при закрытии сокета пользователя: %s\n", err.Error())
	}
	user.state = Closed

	fmt.Printf("Принудительно закрыто соединение с пользователем [%d]\n", user.id)
}

func handleConnection(conn net.Conn, messageChannel chan Packet, bufferPool *BufferPool, id int) {
	user := NewUser(conn, id)

	go func() {
		for user.state != Closed {
			for pooledBuffer := range user.sendQueue {
				buffer := pooledBuffer.buffer
				_, err := conn.Write(buffer.data[:buffer.dataLength])
				if err != nil {
					fmt.Println("Ошибка отправки данных:", err)
					user.closeConnection()
				}
			}
		}
	}()

	go func() {
		for user.state != Closed {
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
	}()
}
