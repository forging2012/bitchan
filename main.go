package main

import (
	"bytes"
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
)

var defaultName = flag.String("defaultName", "名無しさん", "")
var gatewayPort = flag.Int("gatewayPort", 8080, "")
var serventPort = flag.Int("serventPort", 8686, "")
var initNodes = flag.String("initNodes", "", "")
var dhtBucketSize = flag.Int("dhtBucketSize", 20, "Size of k-bucket in Kademlia DHT.")
var dumpMessage = flag.Bool("dumpMessage", false, "Dump all the BitchanMessage")
var mining = flag.Bool("mining", true, "Mine block")

type BoardListItem struct {
	Name string
	Id   string // Max 16 chars
}

var boards = []BoardListItem{{Name: "ビットちゃん板", Id: "bitchan"}}

// - Write transaction part
// - Write mining part
// v Write simple unstructured network part
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
	Index     int
	BoardId   string
	Hash      string
	Title     string
	Posts     []Post
	IsLoading bool
}

type Post struct {
	Index     int
	Name      string
	Mail      string
	Timestamp int64
	Content   string
	IsLoading bool
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
	LastBlock      pb.BlockHash
	DB             *leveldb.DB
	TemporaryBlock *pb.Block
	BlockHeight    map[pb.BlockHash]int
	IsInBlockchain map[pb.TransactionHash]bool
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
		return nil, errors.New("Header: " + err.Error())
	}
	err = proto.Unmarshal(data, &block.BlockHeader)
	if err != nil {
		return nil, err
	}
	bodyKey := append([]byte(BlockBodyPrefix), block.BodyHash[:]...)
	data, err = b.DB.Get(bodyKey, nil)
	if err != nil {
		return nil, errors.New("Body: " + err.Error())
	}
	err = proto.Unmarshal(data, &block.BlockBody)
	if err != nil {
		return nil, err
	}
	for _, transactionHash := range block.TransactionHashes {
		transactionKey := append([]byte(TransactionPrefix), transactionHash...)
		data, err = b.DB.Get(transactionKey, nil)
		if err != nil {
			return nil, errors.New("Transaction: " + err.Error())
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

	blockchain.UpdateLastBlock()

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

func (b *Blockchain) HasData(hash []byte) bool {
	storedValue, err := b.GetData(hash)
	if err != nil {
		log.Fatalln(err)
	}
	return storedValue != nil
}

func (b *Blockchain) GetData(hash []byte) (*pb.StoredValue, error) {
	prefixes := []struct {
		Prefix   string
		DataType pb.DataType
	}{
		{BlockHeaderPrefix, pb.DataType_BLOCK_HEADER},
		{BlockBodyPrefix, pb.DataType_BLOCK_BODY},
		{TransactionPrefix, pb.DataType_TRANSACTION},
		{PostPrefix, pb.DataType_POST}}

	for _, prefix := range prefixes {
		key := append([]byte(prefix.Prefix), hash...)
		ok, err := b.DB.Has(key, nil)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		data, err := b.DB.Get(key, nil)
		if err != nil {
			return nil, err
		}

		storedValue := &pb.StoredValue{
			Data:     data,
			DataType: prefix.DataType}
		return storedValue, nil
	}

	return nil, nil
}

func (b *Blockchain) PutData(storedValue *pb.StoredValue) error {
	prefixes := []struct {
		Prefix   string
		DataType pb.DataType
	}{
		{BlockHeaderPrefix, pb.DataType_BLOCK_HEADER},
		{BlockBodyPrefix, pb.DataType_BLOCK_BODY},
		{TransactionPrefix, pb.DataType_TRANSACTION},
		{PostPrefix, pb.DataType_POST}}

	key := []byte{}
	for _, prefix := range prefixes {
		if prefix.DataType == storedValue.DataType {
			key = []byte(prefix.Prefix)
		}
	}
	if len(key) == 0 {
		return errors.New("wtf")
	}

	hash := pb.Hash160(storedValue.Data)
	key = append(key, hash...)

	err := b.DB.Put(key, storedValue.Data, nil)
	if err != nil {
		return err
	}

	if storedValue.DataType == pb.DataType_TRANSACTION {
		var transaction pb.Transaction
		err = proto.Unmarshal(storedValue.Data, &transaction)
		if err != nil {
			return err
		}
		var transactionHash pb.TransactionHash
		copy(transactionHash[:], hash)
		if !b.IsInBlockchain[transactionHash] {
			b.TemporaryBlock.Transactions = append(b.TemporaryBlock.Transactions, &transaction)
		}

		b.NewTemporaryBlock()
	} else if storedValue.DataType == pb.DataType_BLOCK_BODY {
		var body pb.BlockBody
		err = proto.Unmarshal(storedValue.Data, &body)
		if err != nil {
			return err
		}
		for _, hash := range body.TransactionHashes {
			var transactionHash pb.TransactionHash
			copy(transactionHash[:], hash)
			b.IsInBlockchain[transactionHash] = true
		}

		b.NewTemporaryBlock()
	}

	return nil
}

func GenesisBlock() *pb.Block {
	block := &pb.Block{}
	block.UpdateBodyHash()

	actualHash := block.BlockHeader.Hash()

	predefinedHash, err := hex.DecodeString("4cccf46a7813de0f9ffc705f51f23aca8c7d009f")
	if err != nil {
		log.Fatalln(err)
	}

	if !bytes.Equal(predefinedHash, actualHash[:]) {
		log.Fatalln("cannot generate genesis block")
	}
	return block
}

func (b *Blockchain) Init() {
	b.IsInBlockchain = make(map[pb.TransactionHash]bool)

	_, err := os.Stat("bitchan.leveldb")
	firstTime := err != nil

	b.DB, err = leveldb.OpenFile("bitchan.leveldb", nil)
	if err != nil {
		log.Fatalln(err)
	}

	iter := b.DB.NewIterator(util.BytesPrefix([]byte(BlockBodyPrefix)), nil)
	for iter.Next() {
		_, v := iter.Key(), iter.Value()
		body := &pb.BlockBody{}
		err := proto.Unmarshal(v, body)
		if err != nil {
			log.Fatalln(err)
		}

		for _, hash := range body.TransactionHashes {
			var transactionHash pb.TransactionHash
			copy(transactionHash[:], hash)
			b.IsInBlockchain[transactionHash] = true
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		log.Fatalln(err)
	}


	if firstTime {
		b.PutBlock(GenesisBlock())
	}

	b.UpdateLastBlock()
	b.NewTemporaryBlock()
}

func (b *Blockchain) UpdateLastBlock() {
	previousLastBlock := b.LastBlock

	forwardEdges := make(map[pb.BlockHash][]pb.BlockHash)

	iter := b.DB.NewIterator(util.BytesPrefix([]byte(BlockHeaderPrefix)), nil)
	for iter.Next() {
		_, v := iter.Key(), iter.Value()
		blockHeader := &pb.BlockHeader{}
		err := proto.Unmarshal(v, blockHeader)
		if err != nil {
			log.Fatalln(err)
		}

		if len(blockHeader.PreviousBlockHeaderHash) > 0 {
			var previousBlockHash pb.BlockHash
			copy(previousBlockHash[:], blockHeader.PreviousBlockHeaderHash)

			if _, ok := forwardEdges[previousBlockHash]; !ok {
				forwardEdges[previousBlockHash] = []pb.BlockHash{}
			}

			forwardEdges[previousBlockHash] = append(
				forwardEdges[previousBlockHash],
				blockHeader.Hash())
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		log.Fatalln(err)
	}

	b.BlockHeight = make(map[pb.BlockHash]int)
	b.BlockHeight[GenesisBlock().BlockHeader.Hash()] = 0
	nexts := forwardEdges[GenesisBlock().BlockHeader.Hash()]
	for i := 1; len(nexts) > 0; i++ {
		for _, next := range nexts {
			b.BlockHeight[next] = i
		}

		updatedNexts := []pb.BlockHash{}
		for _, next := range nexts {
			updatedNexts = append(updatedNexts, forwardEdges[next]...)
		}
		nexts = updatedNexts
	}

	longestHeight := -1
	for hash, height := range b.BlockHeight {
		if height > longestHeight {
			b.LastBlock = hash
			longestHeight = height
		}
	}

	// TODO(tetsui): mark orphaned blocks

	if previousLastBlock != b.LastBlock {
		log.Println(
			"Current block height is ", b.BlockHeight[b.LastBlock],
			" (", hex.EncodeToString(b.LastBlock[:]), ")")
		b.NewTemporaryBlock()
	}
}

func (b *Blockchain) NewTemporaryBlock() {
	previousTemporaryBlock := b.TemporaryBlock

	b.TemporaryBlock = &pb.Block{
		BlockHeader: pb.BlockHeader{
			PreviousBlockHeaderHash: b.LastBlock[:],
			Timestamp:               time.Now().Unix()},
		Transactions: []*pb.Transaction{}}

	isDuplicate := make(map[pb.TransactionHash]bool)
	if previousTemporaryBlock != nil {
		for _, transaction := range previousTemporaryBlock.Transactions {
			if !b.IsInBlockchain[transaction.Hash()] && !isDuplicate[transaction.Hash()] {
				b.TemporaryBlock.Transactions =
					append(b.TemporaryBlock.Transactions, transaction)
				isDuplicate[transaction.Hash()] = true
			}
		}
	}

	// TODO(tetsui): append transactions contained in orphaned blocks

	b.TemporaryBlock.UpdateBodyHash()
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
	temporaryBlock := b.TemporaryBlock
L:
	for {
		var block *pb.Block
		var err error
		if temporaryBlock != nil {
			block = temporaryBlock
			temporaryBlock = nil
		} else {
			block, err = b.GetBlock(currentHash)
			if err != nil {
				log.Fatalln("invalid blockchain: ", err)
			}
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
	temporaryBlock := b.TemporaryBlock
	for {
		var block *pb.Block
		var err error
		if temporaryBlock != nil {
			block = temporaryBlock
			temporaryBlock = nil
		} else {
			block, err = b.GetBlock(currentHash)
			if err != nil {
				log.Fatalln("invalid blockchain: ", err)
			}
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
	temporaryBlock := b.TemporaryBlock
	for {
		var block *pb.Block
		var err error
		if temporaryBlock != nil {
			block = temporaryBlock
			temporaryBlock = nil
		} else {
			block, err = b.GetBlock(currentHash)
			if err != nil {
				log.Fatalln("invalid blockchain: ", err)
			}
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
	isLoadings := []bool{}
	for _, postHash := range postHashes {
		post, err := b.GetPost(postHash)
		if err != nil {
			post = &pb.Post{}
		}
		posts = append(posts, post)
		isLoadings = append(isLoadings, err != nil)
	}
	if len(posts) == 0 {
		return nil, errors.New("invalid thread")
	}

	threadTitle := posts[0].ThreadTitle
	if isLoadings[0] {
		threadTitle = "(読み込み中...)"
	}

	thread := &Thread{
		Index:     1,
		BoardId:   board.Id,
		Hash:      hex.EncodeToString(threadHash[:]),
		Title:     threadTitle,
		Posts:     []Post{},
		IsLoading: isLoadings[0]}

	for i, post := range posts {
		thread.Posts = append(thread.Posts, Post{
			Index:     i + 1,
			Name:      post.Name,
			Mail:      post.Mail,
			Timestamp: post.Timestamp,
			Content:   post.Content,
			IsLoading: isLoadings[i]})
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
var servent Servent

func formatTimestamp(timestamp int64) string {
	return time.Unix(timestamp, 0).Format("2006/01/02 15:04:05")
}

func formatPost(text string) template.HTML {
	return template.HTML(strings.Replace(template.HTMLEscapeString(text), "\n", "<br>", -1))
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/ba.gif" {
		http.ServeFile(w, r, "ba.gif")
		return
	}

	funcMap := template.FuncMap{
		"formatTimestamp": formatTimestamp,
		"formatPost":      formatPost}

	threadMatch := regexp.MustCompile("^/test/read\\.cgi/([a-zA-Z0-9]+)/([a-fA-F0-9]+)/?").FindStringSubmatch(r.URL.Path)
	boardMatch := regexp.MustCompile("^/([a-zA-Z0-9]+)/?$").FindStringSubmatch(r.URL.Path)

	if r.Method == "GET" && r.URL.Path == "/" {
		tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles("index.html"))
		tmpl.Execute(w, boards)
	} else if r.Method == "GET" && len(boardMatch) == 2 {
		boardName := boardMatch[1]

		for _, postHash := range blockchain.ListPostHashOfBoard(boardName) {
			servent.Request(postHash[:])
		}

		// TODO
		time.Sleep(20 * time.Millisecond)

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

		for _, postHash := range blockchain.ListPostHashOfThread(boardName, threadHash) {
			servent.Request(postHash[:])
		}

		// TODO
		time.Sleep(20 * time.Millisecond)

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

		data, err := proto.Marshal(transaction)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = blockchain.PutData(&pb.StoredValue{
			DataType: pb.DataType_TRANSACTION,
			Data:     data})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		servent.NotifyTransaction(transaction.Hash())

		tmpl := template.Must(template.New("post.html").Funcs(funcMap).ParseFiles("post.html"))
		tmpl.Execute(w, map[string]string{"BoardId": r.FormValue("boardId")})
	} else {
		http.NotFound(w, r)
	}
}

type Servent struct {
	Nodes  []*pb.Node
	NodeId []byte
}

func (s *Servent) RunMining() {
	if !*mining {
		return
	}
	block := *blockchain.TemporaryBlock

	statThreshold := 100000
	lastTime := time.Now()
	for i := 0; true; i++ {
		block.Nonce++
		hash := block.MiningHash()

		if hash[0] == 0 && hash[1] == 0 && hash[2] == 0 {
			log.Println("Block Mined!", hex.EncodeToString(hash[:]))
			headerData, err := proto.Marshal(&block.BlockHeader)
			if err != nil {
				log.Fatalln(err)
			}
			bodyData, err := proto.Marshal(&block.BlockBody)
			if err != nil {
				log.Fatalln(err)
			}
			blockchain.PutData(&pb.StoredValue{
				DataType: pb.DataType_BLOCK_HEADER,
				Data: headerData})
			blockchain.PutData(&pb.StoredValue{
				DataType: pb.DataType_BLOCK_BODY,
				Data: bodyData})
			blockchain.UpdateLastBlock()

			s.NotifyBlock(block.BlockHeader.Hash())
			blockchain.NewTemporaryBlock()
			block = *blockchain.TemporaryBlock
		}

		if i > statThreshold {
			currentTime := time.Now()
			took := currentTime.Sub(lastTime).Seconds()
			if false {
				log.Println("mining... ", float64(statThreshold)/took, "hash/s")
			}
			i = 0
			lastTime = currentTime

			nonce := block.Nonce
			block = *blockchain.TemporaryBlock
			block.Nonce = nonce
		}
	}
}

func (s *Servent) RegularlyPing() {
	s.Nodes = []*pb.Node{}
	for _, initNode := range strings.Split(*initNodes, ",") {
		if len(initNode) > 0 {
			s.Nodes = append(s.Nodes, &pb.Node{
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
			time.Sleep(30 * time.Second)
		}
		time.Sleep(time.Second)
	}
}

func (s *Servent) PickFromKBuckets(targetId []byte) []*pb.Node {
	// TODO(tetsui):
	return s.Nodes
}

func (s *Servent) StoreToKBuckets(nodes []*pb.Node) {
	// TODO(tetsui):
}

func (s *Servent) Request(targetId []byte) {
	if blockchain.HasData(targetId) {
		return
	}

	for _, node := range s.PickFromKBuckets(targetId) {
		msg := &pb.BitchanMessage{TargetId: targetId, FindValue: true}
		err := s.SendBitchanMessage(node.Address, msg)
		if err != nil {
			log.Fatalln(err)
		}
	}
}

func (s *Servent) RequestMissing(storedValue *pb.StoredValue) error {
	if storedValue.DataType == pb.DataType_BLOCK_HEADER {
		var blockHeader pb.BlockHeader
		err := proto.Unmarshal(storedValue.Data, &blockHeader)
		if err != nil {
			return err
		}

		s.Request(blockHeader.PreviousBlockHeaderHash)
		s.Request(blockHeader.BodyHash)
	} else if storedValue.DataType == pb.DataType_BLOCK_BODY {
		var blockBody pb.BlockBody
		err := proto.Unmarshal(storedValue.Data, &blockBody)
		if err != nil {
			return err
		}

		for _, transactionHash := range blockBody.TransactionHashes {
			s.Request(transactionHash)
		}
	}
	return nil
}

func (s *Servent) Run() {
	go s.RunMining()
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
		if !bytes.Equal(expected_hash, pb.Hash256(content)[0:4]) {
			log.Fatalln("invalid message hash")
		}

		var msg pb.BitchanMessage
		err = proto.Unmarshal(content, &msg)
		if err != nil {
			log.Fatalln(err)
		}
		if *dumpMessage {
			log.Println(addr.String(), "says", msg.String())
		}
		remoteAddr := fmt.Sprintf("%s:%d", addr.IP.String(), msg.OwnPort)

		// FIND_NODE or FIND_VALUE requested.
		if len(msg.TargetId) > 0 {
			replyMsg := &pb.BitchanMessage{}
			storedValue, err := blockchain.GetData(msg.TargetId)
			if err != nil {
				log.Fatalln(err)
			}
			if storedValue == nil && *dumpMessage {
				log.Println("key not found")
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

			if msg.StoredValue.DataType == pb.DataType_BLOCK_HEADER {
				blockchain.UpdateLastBlock()
			}

			s.RequestMissing(msg.StoredValue)
		}

		// inv of Bitcoin
		for _, notifiedHash := range msg.NotifiedHashes {
			storedValue, err := blockchain.GetData(notifiedHash.Hash)
			if err != nil {
				log.Fatalln(err)
			}
			if storedValue == nil {
				replyMsg := &pb.BitchanMessage{
					TargetId:  notifiedHash.Hash,
					FindValue: true}
				s.SendBitchanMessage(remoteAddr, replyMsg)
			}
		}

		// When that is ping, then pong.
		if msg.IsPing {
			replyMsg := &pb.BitchanMessage{
				NotifiedHashes: []*pb.NotifiedHash{
					&pb.NotifiedHash{
						DataType: pb.DataType_BLOCK_HEADER,
						Hash:     blockchain.LastBlock[:]}}}
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
			s.Nodes = append(s.Nodes, &pb.Node{Address: remoteAddr, NodeId: msg.OwnNodeId})
		}
	}
}

func (s *Servent) SendBitchanMessage(address string, msg *pb.BitchanMessage) error {
	msg.OwnPort = int32(*serventPort)

	if *dumpMessage {
		log.Println("I say", msg.String())
	}

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
	copy(dataHash[:], pb.Hash256(data)[0:4])

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

func (s *Servent) NotifyTransaction(hash pb.TransactionHash) {
	msg := &pb.BitchanMessage{
		NotifiedHashes: []*pb.NotifiedHash{
			&pb.NotifiedHash{
				DataType: pb.DataType_TRANSACTION,
				Hash:     hash[:]}}}
	for _, node := range s.Nodes {
		s.SendBitchanMessage(node.Address, msg)
	}
}

func (s *Servent) NotifyBlock(hash pb.BlockHash) {
	msg := &pb.BitchanMessage{
		NotifiedHashes: []*pb.NotifiedHash{
			&pb.NotifiedHash{
				DataType: pb.DataType_BLOCK_HEADER,
				Hash:     hash[:]}}}
	for _, node := range s.Nodes {
		s.SendBitchanMessage(node.Address, msg)
	}
}

func main() {
	flag.Parse()

	blockchain.Init()
	defer blockchain.Close()

	go servent.Run()

	log.Printf("Gateway on http://localhost:%d/", *gatewayPort)
	http.HandleFunc("/", httpHandler)
	log.Fatalln(http.ListenAndServe(fmt.Sprintf(":%d", *gatewayPort), nil))
}
