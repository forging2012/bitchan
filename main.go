package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/golang/protobuf/proto"
	pb "github.com/peryaudo/bitchan/bitchan_pb"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"golang.org/x/crypto/ripemd160"
)

var defaultName = flag.String("defaultName", "名無しさん", "")
var gatewayPort = flag.Int("gatewayPort", 8080, "")
var serventPort = flag.Int("serventPort", 8686, "")
var initNodes = flag.String("initNodes", "", "")
var dhtBucketSize = flag.Int("dhtBucketSize", 20, "Size of k-bucket in Kademlia DHT.")

type BoardListItem struct {
	Name string
	Id   string // Max 16 chars
}

var boards = []BoardListItem{{Name: "ビットちゃん板", Id: "bitchan"}}

// - Write transaction part
// - Write mining part
// - Write simple unstructured network part
// - Write DHT network part

// https://github.com/grpc/grpc-go/blob/master/examples/helloworld/greeter_client/main.go
// http://www.grpc.io/docs/tutorials/basic/go.html
// https://pdos.csail.mit.edu/~petar/papers/maymounkov-kademlia-lncs.pdf

type Board struct {
	Id        string
	BoardName string
	Threads   []Thread
}

type Thread struct {
	Index   int
	BoardId string
	Hash    string
	Title   string
	Posts   []Post
}

type Post struct {
	Index     int
	Name      string
	Mail      string
	Timestamp int64
	Content   string
}

type PostCandidate struct {
	Name        string
	Mail        string
	Content     string
	ThreadHash  pb.TransactionHash
	ThreadTitle string
	BoardId     string
}

type Blockchain struct {
	LastBlock pb.BlockHash
	DB        *leveldb.DB
}

const (
	BlockHeaderPrefix = "BLKH"
	BlockBodyPrefix   = "BLKB"
	TransactionPrefix = "TRAN"
	PostPrefix        = "POST"
)

func (b *Blockchain) Close() { b.DB.Close() }

func (b *Blockchain) GetBlock(blockHash pb.BlockHash) (*pb.Block, error) {
	block := &pb.Block{}
	headerKey := append([]byte(BlockHeaderPrefix), blockHash[:]...)
	data, err := b.DB.Get(headerKey, nil)
	if err != nil {
		return nil, err
	}
	err = proto.Unmarshal(data, &block.BlockHeader)
	if err != nil {
		return nil, err
	}
	bodyKey := append([]byte(BlockBodyPrefix), block.BodyHash[:]...)
	data, err = b.DB.Get(bodyKey, nil)
	if err != nil {
		return nil, err
	}
	err = proto.Unmarshal(data, &block.BlockBody)
	if err != nil {
		return nil, err
	}
	for _, transactionHash := range block.TransactionHashes {
		transactionKey := append([]byte(TransactionPrefix), transactionHash...)
		data, err = b.DB.Get(transactionKey, nil)
		if err != nil {
			return nil, err
		}
		var transaction pb.Transaction
		err = proto.Unmarshal(data, &transaction)
		block.Transactions = append(block.Transactions, &transaction)
	}
	return block, nil
}

func (b *Blockchain) PutBlock(block *pb.Block) error {
	// TODO(tetsui): This func signature is not safe for malleability.
	data, err := proto.Marshal(&block.BlockHeader)
	if err != nil {
		return err
	}
	bh := block.BlockHeader.Hash()
	key := append([]byte(BlockHeaderPrefix), bh[:]...)
	err = b.DB.Put(key, data, nil)
	if err != nil {
		return err
	}

	data, err = proto.Marshal(&block.BlockBody)
	if err != nil {
		return err
	}
	bb := block.BlockBody.Hash()
	key = append([]byte(BlockBodyPrefix), bb[:]...)
	err = b.DB.Put(key, data, nil)
	if err != nil {
		return err
	}

	for _, transaction := range block.Transactions {
		data, err = proto.Marshal(transaction)
		if err != nil {
			return err
		}
		th := transaction.Hash()
		key = append([]byte(TransactionPrefix), th[:]...)
		err = b.DB.Put(key, data, nil)
	}
	return nil
}

func (b *Blockchain) GetPost(postHash pb.PostHash) (*pb.Post, error) {
	key := append([]byte(PostPrefix), postHash[:]...)
	data, err := b.DB.Get(key, nil)
	if err != nil {
		return nil, err
	}
	post := &pb.Post{}
	err = proto.Unmarshal(data, post)
	if err != nil {
		return nil, err
	}
	return post, nil
}

func (b *Blockchain) PutPost(post *pb.Post) error {
	// TODO(tetsui): This func signature is not safe for malleability.

	data, err := proto.Marshal(post)
	if err != nil {
		return err
	}
	h := post.Hash()
	key := append([]byte(PostPrefix), h[:]...)
	err = b.DB.Put(key, data, nil)
	if err != nil {
		return err
	}
	return nil
}

func (b *Blockchain) GetData(hash []byte) (*pb.StoredValue, error) {
	prefixes := []struct{
			Prefix string
			DataType pb.DataType
		}{
			{BlockHeaderPrefix,	pb.DataType_BLOCK_HEADER},
			{BlockBodyPrefix,	pb.DataType_BLOCK_BODY},
			{TransactionPrefix,	pb.DataType_TRANSACTION},
			{PostPrefix,		pb.DataType_POST}}

	for _, prefix := range prefixes {
		key := append([]byte(prefix.Prefix), hash...)
		ok, err := b.DB.Has(key, nil)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		data, err := b.DB.Get(key,  nil)
		if err != nil {
			return nil, err
		}

		storedValue := &pb.StoredValue{
			Data: data,
			DataType: prefix.DataType}
		return storedValue, nil
	}

	return nil, nil
}

func (b *Blockchain) PutData(storedValue *pb.StoredValue) error {
	prefixes := []struct{
			Prefix string
			DataType pb.DataType
		}{
			{BlockHeaderPrefix,	pb.DataType_BLOCK_HEADER},
			{BlockBodyPrefix,	pb.DataType_BLOCK_BODY},
			{TransactionPrefix,	pb.DataType_TRANSACTION},
			{PostPrefix,		pb.DataType_POST}}

	key := []byte{}
	for _, prefix := range prefixes {
		if prefix.DataType == storedValue.DataType {
			key = []byte(prefix.Prefix)
		}
	}
	if len(key) == 0 {
		return errors.New("wtf")
	}

	h := ripemd160.New()
	h.Write(storedValue.Data)

	key = append(key, h.Sum(nil)...)

	return b.DB.Put(key, storedValue.Data, nil)
}

func (b *Blockchain) Init() {
	_, err := os.Stat("bitchan.leveldb")
	firstTime := err != nil

	b.DB, err = leveldb.OpenFile("bitchan.leveldb", nil)
	if err != nil {
		log.Fatalln(err)
	}

	if firstTime {
		p1, t1, _ := b.CreatePost(&PostCandidate{
			Name:        "名無しさん",
			Mail:        "",
			Content:     "1got",
			ThreadHash:  pb.TransactionHash{},
			ThreadTitle: "ほげほげスレ",
			BoardId:     "bitchan"})
		p2, t2, _ := b.CreatePost(&PostCandidate{
			Name:        "名無しさん",
			Mail:        "sage",
			Content:     "糞スレsage",
			ThreadHash:  t1.Hash(),
			ThreadTitle: "",
			BoardId:     "bitchan"})
		p3, t3, _ := b.CreatePost(&PostCandidate{
			Name:        "名無しさん",
			Mail:        "",
			Content:     "てますか？",
			ThreadHash:  pb.TransactionHash{},
			ThreadTitle: "はげ",
			BoardId:     "bitchan"})
		b.PutPost(p1)
		b.PutPost(p2)
		b.PutPost(p3)

		block := pb.Block{}
		block.Transactions = []*pb.Transaction{t1, t2, t3}
		block.UpdateBodyHash()

		b.PutBlock(&block)
	}

	iter := b.DB.NewIterator(util.BytesPrefix([]byte(BlockHeaderPrefix)), nil)
	blockHashes := []pb.BlockHash{}
	blockRef := make(map[pb.BlockHash]bool)
	for iter.Next() {
		_, v := iter.Key(), iter.Value()
		blockHeader := &pb.BlockHeader{}
		err = proto.Unmarshal(v, blockHeader)
		if err != nil {
			log.Fatalln(err)
		}
		blockHashes = append(blockHashes, blockHeader.Hash())
		if len(blockHeader.PreviousBlockHeaderHash) > 0 {
			var blockHash pb.BlockHash
			copy(blockHash[:], blockHeader.PreviousBlockHeaderHash)
			blockRef[blockHash] = true
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		log.Fatalln(err)
	}

	for _, blockHash := range blockHashes {
		if !blockRef[blockHash] {
			b.LastBlock = blockHash
			break
		}
	}

	log.Println("Current latest block is ", hex.EncodeToString(b.LastBlock[:]))
}

func (b *Blockchain) CreatePost(in *PostCandidate) (post *pb.Post, transaction *pb.Transaction, err error) {
	post = &pb.Post{
		Name:      in.Name,
		Mail:      in.Mail,
		Content:   in.Content,
		Timestamp: time.Now().Unix()}
	if in.ThreadTitle != "" {
		post.ThreadTitle = in.ThreadTitle
	}

	postHash := post.Hash()
	transaction = &pb.Transaction{
		BoardId:   in.BoardId,
		PostHash:  postHash[:],
		Downvoted: in.Mail == "sage"}
	if in.ThreadTitle == "" {
		transaction.ThreadTransactionHash = in.ThreadHash[:]
	}

	return
}

func (b *Blockchain) ListPostHashOfThread(boardId string, threadHash pb.TransactionHash) []pb.PostHash {
	results := []pb.PostHash{}
	currentHash := b.LastBlock
L:
	for {
		block, err := b.GetBlock(currentHash)
		if err != nil {
			log.Fatalln("invalid blockchain: ", err)
		}

		for i := len(block.Transactions) - 1; i >= 0; i-- {
			transaction := block.Transactions[i]
			if transaction.BoardId != boardId {
				continue
			}
			if !transaction.IsInThread(threadHash) {
				continue
			}

			var postHash pb.PostHash
			copy(postHash[:], transaction.PostHash)
			results = append(results, postHash)

			if len(transaction.ThreadTransactionHash) == 0 {
				break L
			}
		}

		// Genesis block.
		if len(block.PreviousBlockHeaderHash) == 0 {
			break
		}
		copy(currentHash[:], block.PreviousBlockHeaderHash)
	}

	reversed := []pb.PostHash{}
	for i := len(results) - 1; i >= 0; i-- {
		reversed = append(reversed, results[i])
	}
	return reversed
}

func (b *Blockchain) ListPostHashOfBoard(boardId string) []pb.PostHash {
	results := []pb.PostHash{}
	currentHash := b.LastBlock
	for {
		block, err := b.GetBlock(currentHash)
		if err != nil {
			log.Fatalln("invalid blockchain: ", err)
		}

		for i := len(block.Transactions) - 1; i >= 0; i-- {
			transaction := block.Transactions[i]
			if transaction.BoardId != boardId {
				continue
			}

			var postHash pb.PostHash
			copy(postHash[:], transaction.PostHash)
			results = append(results, postHash)
		}

		// Genesis block.
		if len(block.PreviousBlockHeaderHash) == 0 {
			break
		}
		copy(currentHash[:], block.PreviousBlockHeaderHash)
	}

	return results
}

func (b *Blockchain) ListThreadHashOfBoard(boardId string) []pb.TransactionHash {
	results := []pb.TransactionHash{}
	currentHash := b.LastBlock
	for {
		block, err := b.GetBlock(currentHash)
		if err != nil {
			log.Fatalln("invalid blockchain: ", err)
		}

		for i := len(block.Transactions) - 1; i >= 0; i-- {
			transaction := block.Transactions[i]
			if transaction.BoardId != boardId {
				continue
			}
			if len(transaction.ThreadTransactionHash) > 0 {
				continue
			}

			results = append(results, transaction.Hash())
		}

		// Genesis block.
		if len(block.PreviousBlockHeaderHash) == 0 {
			break
		}
		copy(currentHash[:], block.PreviousBlockHeaderHash)
	}
	return results
}

func (b *Blockchain) ConstructThread(boardId string, threadHash pb.TransactionHash) (*Thread, error) {
	found := false
	board := BoardListItem{}
	for _, v := range boards {
		if v.Id == boardId {
			found = true
			board = v
			break
		}
	}
	if !found {
		return nil, errors.New("invalid board ID")
	}

	postHashes := b.ListPostHashOfThread(boardId, threadHash)
	posts := []*pb.Post{}
	for _, postHash := range postHashes {
		post, err := b.GetPost(postHash)
		if err != nil {
			post = &pb.Post{}
		}
		posts = append(posts, post)
	}
	if len(posts) == 0 || posts[0].ThreadTitle == "" {
		return nil, errors.New("invalid thread")
	}

	thread := &Thread{
		Index:   1,
		BoardId: board.Id,
		Hash:    hex.EncodeToString(threadHash[:]),
		Title:   posts[0].ThreadTitle,
		Posts:   []Post{}}

	for i, post := range posts {
		thread.Posts = append(thread.Posts, Post{
			Index:     i + 1,
			Name:      post.Name,
			Mail:      post.Mail,
			Timestamp: post.Timestamp,
			Content:   post.Content})
	}

	return thread, nil
}

func (b *Blockchain) ConstructBoard(boardId string) (*Board, error) {
	found := false
	boardMetadata := BoardListItem{}
	for _, v := range boards {
		if v.Id == boardId {
			found = true
			boardMetadata = v
			break
		}
	}
	if !found {
		return nil, errors.New("invalid board ID")
	}

	board := &Board{
		Id:        boardId,
		BoardName: boardMetadata.Name,
		Threads:   []Thread{}}

	threadHashes := b.ListThreadHashOfBoard(boardId)
	for i, threadHash := range threadHashes {
		thread, err := b.ConstructThread(boardId, threadHash)
		if err != nil {
			return nil, err
		}
		thread.Index = i + 1
		board.Threads = append(board.Threads, *thread)
	}

	return board, nil
}

var blockchain Blockchain

func formatTimestamp(timestamp int64) string {
	return time.Unix(timestamp, 0).Format("2006/01/02 15:04:05")
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	funcMap := template.FuncMap{"formatTimestamp": formatTimestamp}

	threadMatch := regexp.MustCompile("^/test/read\\.cgi/([a-zA-Z0-9]+)/([a-fA-F0-9]+)/?").FindStringSubmatch(r.URL.Path)
	boardMatch := regexp.MustCompile("^/([a-zA-Z0-9]+)/?$").FindStringSubmatch(r.URL.Path)

	if r.Method == "GET" && r.URL.Path == "/" {
		tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles("index.html"))
		tmpl.Execute(w, boards)
	} else if r.Method == "GET" && len(boardMatch) == 2 {
		boardName := boardMatch[1]

		board, err := blockchain.ConstructBoard(boardName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tmpl := template.Must(template.New("board.html").Funcs(funcMap).ParseFiles("board.html"))
		tmpl.Execute(w, board)
	} else if r.Method == "GET" && len(threadMatch) == 3 {
		boardName := threadMatch[1]
		threadHashSlice, err := hex.DecodeString(threadMatch[2])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var threadHash pb.TransactionHash
		if len(threadHashSlice) != len(threadHash) {
			http.Error(w, "invalid thread hash length", http.StatusInternalServerError)
			return
		}
		copy(threadHash[:], threadHashSlice)

		thread, err := blockchain.ConstructThread(boardName, threadHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tmpl := template.Must(template.New("thread.html").Funcs(funcMap).ParseFiles("thread.html"))
		tmpl.Execute(w, thread)
	} else if r.Method == "POST" && r.URL.Path == "/test/bbs.cgi" {
		threadHashSlice, err := hex.DecodeString(r.FormValue("threadHash"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var threadHash pb.TransactionHash
		if r.FormValue("threadTitle") == "" && len(threadHashSlice) != len(threadHash) {
			http.Error(w, "invalid thread hash length", http.StatusInternalServerError)
			return
		}
		copy(threadHash[:], threadHashSlice)

		candidate := &PostCandidate{
			Name:        r.FormValue("postName"),
			Mail:        r.FormValue("mail"),
			Content:     r.FormValue("content"),
			ThreadHash:  threadHash,
			ThreadTitle: r.FormValue("threadTitle"),
			BoardId:     r.FormValue("boardId")}
		if candidate.Name == "" {
			candidate.Name = *defaultName
		}
		post, transaction, err := blockchain.CreatePost(candidate)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		blockchain.PutPost(post)
		block := pb.Block{}
		block.Transactions = []*pb.Transaction{transaction}
		block.UpdateBodyHash()
		prevHash := blockchain.LastBlock
		block.PreviousBlockHeaderHash = prevHash[:]
		blockchain.PutBlock(&block)
		blockchain.LastBlock = block.BlockHeader.Hash()

		tmpl := template.Must(template.New("post.html").Funcs(funcMap).ParseFiles("post.html"))
		tmpl.Execute(w, map[string]string{"BoardId": r.FormValue("boardId")})
	} else {
		http.NotFound(w, r)
	}
}

type Servent struct {
	Nodes []pb.Node
	NodeId []byte
}

func (s *Servent) RegularlyPing() {
	s.Nodes = []pb.Node{}
	for _, initNode := range strings.Split(*initNodes, ",") {
		if len(initNode) > 0 {
			s.Nodes = append(s.Nodes, pb.Node{
				Address: initNode})
		}
	}

	for {
		for _, node := range s.Nodes {
			msg := pb.BitchanMessage{IsPing: true}
			err := s.SendBitchanMessage(node.Address, &msg)
			if err != nil {
				log.Fatalln(err)
			}
			time.Sleep(time.Second)
		}
		time.Sleep(time.Second)
	}
}

func (s *Servent) PickFromKBuckets(targetId []byte) []*pb.Node {
	// TODO(tetsui):
	return []*pb.Node{}
}

func (s *Servent) StoreToKBuckets(nodes []*pb.Node) {
	// TODO(tetsui):
}

func (s *Servent) Run() {
	go s.RegularlyPing()

	log.Printf("Servent on localhost:%d", *serventPort)
	// TODO(tetsui): Reuse connection.
	localAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", *serventPort))
	if err != nil {
		log.Fatalln(err)
	}
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		log.Fatalln(err)
	}

	buf := make([]byte, 1024*1024)

	// TODO(tetsui): do not fatalln
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		addr = addr
		if err != nil {
			log.Fatalln(err)
		}
		if n < 8 {
			log.Fatalln("invalid length ", n)
		}
		expected_len := binary.LittleEndian.Uint32(buf[0:4])
		expected_hash := buf[4:8]
		if n != int(expected_len)+8 {
			log.Fatalln("invalid length ", n, " vs ", expected_len+8)
		}
		content := buf[8 : 8+expected_len]
		// TODO(tetsui): verify hash
		expected_hash = expected_hash

		var msg pb.BitchanMessage
		err = proto.Unmarshal(content, &msg)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println(addr.String(), "says", msg.String())
		remoteAddr := fmt.Sprintf("%s:%d", addr.IP.String(), msg.OwnPort)

		// FIND_NODE or FIND_VALUE requested.
		if len(msg.TargetId) > 0 {
			replyMsg := &pb.BitchanMessage{}
			storedValue, err := blockchain.GetData(msg.TargetId)
			if err != nil {
				log.Fatalln(err)
			}
			if msg.FindValue && storedValue != nil {
				replyMsg.StoredValue = storedValue
			} else {
				replyMsg.Nodes = s.PickFromKBuckets(msg.TargetId)
			}
			s.SendBitchanMessage(remoteAddr, replyMsg)
		}

		// FIND_NODE or FIND_VALUE responded.
		if len(msg.Nodes) > 0 {
			// TODO(tetsui): Store to k-bucket
			s.StoreToKBuckets(msg.Nodes)
		}

		// STORE_VALUE
		if msg.StoredValue != nil {
			err = blockchain.PutData(msg.StoredValue)
			if err != nil {
				log.Fatalln(err)
			}
		}
		
		// inv of Bitcoin
		for _, notifiedHash := range msg.NotifiedHashes {
			storedValue, err := blockchain.GetData(notifiedHash.Hash)
			if err != nil {
				log.Fatalln(err)
			}
			if storedValue == nil {
				replyMsg := &pb.BitchanMessage{
					TargetId: notifiedHash.Hash}
				s.SendBitchanMessage(remoteAddr, replyMsg)
			}
		}

		// When that is ping, then pong.
		if msg.IsPing {
			replyMsg := &pb.BitchanMessage{}
			s.SendBitchanMessage(remoteAddr, replyMsg)
		}

		// TODO(tetsui): 
		addrFound := false
		for i := 0; i < len(s.Nodes); i++ {
			if s.Nodes[i].Address == remoteAddr {
				s.Nodes[i].NodeId = msg.OwnNodeId
				addrFound = true
			}
		}
		if !addrFound {
			s.Nodes = append(s.Nodes, pb.Node{Address: remoteAddr, NodeId: msg.OwnNodeId})
		}
	}
}

func (s *Servent) SendBitchanMessage(address string, msg *pb.BitchanMessage) error {
	msg.OwnPort = int32(*serventPort)

	remoteAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}

	conn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	var dataLen [4]byte
	binary.LittleEndian.PutUint32(dataLen[:], uint32(len(data)))
	var dataHash [4]byte
	// TODO(tetsui)

	buf := []byte{}
	buf = append(buf, dataLen[:]...)
	buf = append(buf, dataHash[:]...)
	buf = append(buf, data...)

	_, err = conn.Write(buf)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Parse()

	blockchain.Init()
	defer blockchain.Close()

	var servent Servent
	go servent.Run()

	log.Printf("Gateway on http://localhost:%d/", *gatewayPort)
	http.HandleFunc("/", httpHandler)
	log.Fatalln(http.ListenAndServe(fmt.Sprintf(":%d", *gatewayPort), nil))
}
