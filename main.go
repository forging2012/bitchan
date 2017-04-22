package main

import (
	// "github.com/golang/leveldb"
	// "google.golang.org/grpc"
	// "github.com/peryaudo/bitchan/pb"
	"html/template"
	"log"
	"net/http"
	"time"
	"strings"
)


type BoardListItem struct {
	Name string
	Id string
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
	Id		string
	BoardName       string
	Threads         []Thread
}

type Thread struct {
	Index int
	BoardId string
	Hash string
	Title string
	Posts []Post
}

type Post struct {
	Index     int
	Name      string
	Mail      string
	Timestamp int64
	Content   string
}

func formatTimestamp(timestamp int64) string {
	return time.Unix(timestamp, 0).Format("2006/01/02 15:04:05")
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	funcMap := template.FuncMap{ "formatTimestamp": formatTimestamp}
	hogeThread := Thread{
		Index: 1,
		BoardId: "bitchan",
		Hash: "114514",
		Title: "ほげほげスレ",
		Posts: []Post{
			Post{Index: 1, Name: "名無しさん", Mail: "", Timestamp: 0, Content: "1got"},
			Post{Index: 2, Name: "名無しさん", Mail: "sage", Timestamp: 0, Content: "糞スレsage"}}}

	hageThread := Thread{
		Index: 2,
		BoardId: "bitchan",
		Hash: "114515",
		Title: "はげ",
		Posts: []Post{
			Post{Index: 1, Name: "名無しさん", Mail: "", Timestamp: 0, Content: "てますか？"}}}

	hogeBoard := Board{Id: "bitchan", BoardName: "ビットちゃん板", Threads: []Thread{hogeThread, hageThread}}

	if r.URL.Path == "/" {
		tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles("index.html"))
		tmpl.Execute(w, boards)
	} else if strings.HasPrefix(r.URL.Path, "/test/read.cgi") {
		tmpl := template.Must(template.New("thread.html").Funcs(funcMap).ParseFiles("thread.html"))
		tmpl.Execute(w, hogeThread)
	} else {
		tmpl := template.Must(template.New("board.html").Funcs(funcMap).ParseFiles("board.html"))
		tmpl.Execute(w, hogeBoard)
	}
}

func main() {
	log.Println("Listen on 8080")
	http.HandleFunc("/", httpHandler)
	log.Fatalln(http.ListenAndServe(":8080", nil))
}
