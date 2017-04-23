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
	"errors"
)

const (
	DefaultName = "名無しさん"
)

type BoardListItem struct {
	Name string
	Id   string	// Max 16 chars
}

var boards = []BoardListItem{{Name: "ビットちゃん板", Id: "bitchan"}}

// - Write persistent part
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
	Name string
	Mail string
	Content string
	ThreadHash pb.TransactionHash
	ThreadTitle string
	BoardId string
}

type Blockchain struct {
	Blocks    map[pb.BlockHash]*pb.Block
	Posts     map[pb.PostHash]*pb.Post
	LastBlock pb.BlockHash
}

func (b *Blockchain) Init() {
	b.Blocks = make(map[pb.BlockHash]*pb.Block)
	b.Posts = make(map[pb.PostHash]*pb.Post)

	p1, t1, _ := b.CreatePost(&PostCandidate{
		Name: "名無しさん",
		Mail: "",
		Content: "1got",
		ThreadHash: pb.TransactionHash{},
		ThreadTitle: "ほげほげスレ",
		BoardId: "bitchan"})
	p2, t2, _ := b.CreatePost(&PostCandidate{
		Name: "名無しさん",
		Mail: "sage",
		Content: "糞スレsage",
		ThreadHash: t1.Hash(),
		ThreadTitle: "",
		BoardId: "bitchan"})
	p3, t3, _ := b.CreatePost(&PostCandidate{
		Name: "名無しさん",
		Mail: "",
		Content: "てますか？",
		ThreadHash: pb.TransactionHash{},
		ThreadTitle: "はげ",
		BoardId: "bitchan"})
	b.Posts[p1.Hash()] = p1
	b.Posts[p2.Hash()] = p2
	b.Posts[p3.Hash()] = p3

	block := pb.Block{}
	block.Transactions = []*pb.Transaction{t1, t2, t3}
	block.UpdateBodyHash()

	b.Blocks[block.BlockHeader.Hash()] = &block
	b.LastBlock = block.BlockHeader.Hash()
}

func (b *Blockchain) CreatePost(in *PostCandidate) (post *pb.Post, transaction *pb.Transaction, err error) {
	post = &pb.Post{
		Name: in.Name,
		Mail: in.Mail,
		Content: in.Content,
		Timestamp: time.Now().Unix()}
	if in.ThreadTitle != "" {
		post.ThreadTitle = in.ThreadTitle
	}

	postHash := post.Hash()
	transaction = &pb.Transaction{
		BoardId: in.BoardId,
		PostHash: postHash[:]}
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
		block, ok := b.Blocks[currentHash]
		if !ok {
			log.Fatalln("invalid blockchain")
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
		block, ok := b.Blocks[currentHash]
		if !ok {
			log.Fatalln("invalid blockchain")
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
		block, ok := b.Blocks[currentHash]
		if !ok {
			log.Fatalln("invalid blockchain")
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
		posts = append(posts, b.Posts[postHash])
	}
	if len(posts) == 0 || posts[0].ThreadTitle == "" {
		return nil, errors.New("invalid thread")
	}

	thread := &Thread{
		Index: 1,
		BoardId: board.Id,
		Hash: hex.EncodeToString(threadHash[:]),
		Title: posts[0].ThreadTitle,
		Posts: []Post{}}
	
	for i, post := range posts {
		thread.Posts = append(thread.Posts, Post{
			Index: i + 1,
			Name: post.Name,
			Mail: post.Mail,
			Timestamp: post.Timestamp,
			Content: post.Content})
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
		Id: boardId,
		BoardName: boardMetadata.Name,
		Threads: []Thread{}}

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

	if r.Method =="GET" && r.URL.Path == "/" {
		tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles("index.html"))
		tmpl.Execute(w, boards)
	} else if r.Method =="GET" && len(boardMatch) == 2 {
		boardName := boardMatch[1]

		board, err := blockchain.ConstructBoard(boardName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tmpl := template.Must(template.New("board.html").Funcs(funcMap).ParseFiles("board.html"))
		tmpl.Execute(w, board)
	} else if r.Method =="GET" && len(threadMatch) == 3 {
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
	} else if r.Method =="POST" && r.URL.Path == "/test/bbs.cgi" {
		threadHashSlice, err := hex.DecodeString(r.FormValue("threadHash"))
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

		candidate := &PostCandidate{
			Name: r.FormValue("postName"),
			Mail: r.FormValue("mail"),
			Content: r.FormValue("content"),
			ThreadHash: threadHash,
			ThreadTitle: r.FormValue("threadTitle"),
			BoardId: r.FormValue("boardId")}
		if candidate.Name == "" {
			candidate.Name = DefaultName
		}
		post, transaction, err := blockchain.CreatePost(candidate)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		blockchain.Posts[post.Hash()] = post
		block := pb.Block{}
		block.Transactions = []*pb.Transaction{transaction}
		block.UpdateBodyHash()
		prevHash := blockchain.LastBlock
		block.PreviousBlockHeaderHash = prevHash[:]
		blockchain.Blocks[block.BlockHeader.Hash()] = &block
		blockchain.LastBlock = block.BlockHeader.Hash()

		tmpl := template.Must(template.New("post.html").Funcs(funcMap).ParseFiles("post.html"))
		tmpl.Execute(w, map[string]string{"BoardId": r.FormValue("boardId")})
	} else {
		http.NotFound(w, r)
	}
}

func main() {
	blockchain.Init()

	log.Println("Listen on 8080")
	http.HandleFunc("/", httpHandler)
	log.Fatalln(http.ListenAndServe(":8080", nil))
}
