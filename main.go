package main

import (
	// "github.com/syndtr/goleveldb"
	// "google.golang.org/grpc"
	"encoding/hex"
	pb "github.com/peryaudo/bitchan/bitchan_pb"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"time"
)

type BoardListItem struct {
	Name string
	Id   string
}

var boards = []BoardListItem{{Name: "ビットちゃん板", Id: "bitchan"}}

// - Write converter from blockchain format to these view structs (top and read.cgi)
// - Write bbs.cgi to generate blockchain format
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

type BlockHash [32]byte
type BlockBodyHash [32]byte
type TransactionHash [20]byte
type PostHash [20]byte

type Block struct {
	pb.BlockHeader
	pb.BlockBody
}

type Blockchain struct {
	Blocks    map[BlockHash]Block
	Posts     map[PostHash]pb.Post
	LastBlock BlockHash
}

func (b *Blockchain) ListPostHashOfThread(boardId string, threadHash TransactionHash) []PostHash {
	return []PostHash{}
}

func (b *Blockchain) ListPostHashOfBoard(boardId string) []PostHash {
	return []PostHash{}
}

func (b *Blockchain) ConstructThread(boardId string, threadHash TransactionHash) Thread {
	return Thread{
		Index:   1,
		BoardId: "bitchan",
		Hash:    hex.EncodeToString(threadHash[:]),
		Title:   "ほげほげスレ",
		Posts: []Post{
			Post{Index: 1, Name: "名無しさん", Mail: "", Timestamp: 0, Content: "1got"},
			Post{Index: 2, Name: "名無しさん", Mail: "sage", Timestamp: 0, Content: "糞スレsage"}}}

}

func (b *Blockchain) ConstructBoard(boardId string) Board {
	var threadHash TransactionHash
	hogeThread := Thread{
		Index:   1,
		BoardId: "bitchan",
		Hash:    hex.EncodeToString(threadHash[:]),
		Title:   "ほげほげスレ",
		Posts: []Post{
			Post{Index: 1, Name: "名無しさん", Mail: "", Timestamp: 0, Content: "1got"},
			Post{Index: 2, Name: "名無しさん", Mail: "sage", Timestamp: 0, Content: "糞スレsage"}}}

	hageThread := Thread{
		Index:   2,
		BoardId: "bitchan",
		Hash:    hex.EncodeToString(threadHash[:]),
		Title:   "はげ",
		Posts: []Post{
			Post{Index: 1, Name: "名無しさん", Mail: "", Timestamp: 0, Content: "てますか？"}}}

	return Board{Id: "bitchan", BoardName: "ビットちゃん板", Threads: []Thread{hogeThread, hageThread}}
}

var blockchain Blockchain

func formatTimestamp(timestamp int64) string {
	return time.Unix(timestamp, 0).Format("2006/01/02 15:04:05")
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	funcMap := template.FuncMap{"formatTimestamp": formatTimestamp}

	threadMatch := regexp.MustCompile("^/test/read\\.cgi/([a-zA-Z0-9]+)/([a-fA-F0-9]+)/?").FindStringSubmatch(r.URL.Path)
	boardMatch := regexp.MustCompile("^/([a-zA-Z0-9]+)/?$").FindStringSubmatch(r.URL.Path)

	if r.URL.Path == "/" {
		tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles("index.html"))
		tmpl.Execute(w, boards)
	} else if len(boardMatch) == 2 {
		boardName := boardMatch[1]

		tmpl := template.Must(template.New("board.html").Funcs(funcMap).ParseFiles("board.html"))
		tmpl.Execute(w, blockchain.ConstructBoard(boardName))
	} else if len(threadMatch) == 3 {
		boardName := threadMatch[1]
		threadHashSlice, err := hex.DecodeString(threadMatch[2])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var threadHash TransactionHash
		if len(threadHashSlice) != len(threadHash) {
			http.Error(w, "invalid thread hash length", http.StatusInternalServerError)
			return
		}
		copy(threadHash[:], threadHashSlice[0:len(threadHash)])

		tmpl := template.Must(template.New("thread.html").Funcs(funcMap).ParseFiles("thread.html"))
		tmpl.Execute(w, blockchain.ConstructThread(boardName, threadHash))
	} else if r.URL.Path == "/test/bbs.cgi" {
		http.Error(w, "Not Implemented", http.StatusInternalServerError)
	} else {
		http.NotFound(w, r)
	}
}

func main() {
	log.Println("Listen on 8080")
	http.HandleFunc("/", httpHandler)
	log.Fatalln(http.ListenAndServe(":8080", nil))
}
