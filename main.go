package main

import (
    "time"
    "net/http"
    "fmt"
    "sync"
    "github.com/gorilla/websocket"
)

var (
    // websocket handler
    upgrader = websocket.Upgrader{
        ReadBufferSize: 1024,
        WriteBufferSize: 1024,
        CheckOrigin: func(r *http.Request) bool {
            origin := r.Header.Get("Origin")
            return origin == "http://localhost:8000" || origin == "http://127.0.0.1:8000" // check ip
        },
    }

    // game queue
    queue = make(chan *Player, 1)
)

// values sent to end game
const (
    // NONE = 0
    INGAME_C = 1
    DC_C = 2
    DC_O = 3
)

// values sent by to player to game
const (
    // BUTTON0 = 0
    // BUTTON1 = 1
    // BUTTON2 = 2
    DC = 3
)

// game structure
type Game struct {
    currPlay, oppPlay *Player
    timer *time.Timer
    seq *LList
    prog *LList
}

// linked list for button sequences
type LList struct {
    val byte
    next *LList
}

// player structure
type Player struct {
    wr *Writer
    req chan byte
    inGame bool
}

// prevents concurrent writes
type Writer struct {
    con *websocket.Conn
    mu sync.Mutex
}

func (w Writer) write (msg string) {
    w.mu.Lock()
    defer w.mu.Unlock()
    w.con.WriteMessage(websocket.TextMessage, []byte(msg))
}

func initGame(p1 *Player, p2 *Player) {
    // check for disconnects / leftover buttons
    select {
    case c := <- p1.req:
        if c == DC {
            queue <- p2
            return
        }
    default:
    }

    select {
    case c := <- p2.req:
        if c == DC {
            queue <- p1
            return
        }
    default:
    }

    // create game
    g := Game{}
    // players
    g.currPlay = p1
    g.oppPlay = p2
    // in-game timer
    g.timer = time.NewTimer(30 * time.Second)
    // buttons pressed
    g.seq = &LList{val: 3}
    g.prog = g.seq
    // if game is over
    var over byte
    // send signal to players
    g.currPlay.wr.write("starty")
    g.oppPlay.wr.write("startt")
    g.currPlay.inGame = true
    g.oppPlay.inGame = true
    fmt.Println("game started")
    // loop until game is over
    for {
        select {
        // when timer runs out
        case <-g.timer.C:
            over = INGAME_C
        // move by active player
        case action := <-g.currPlay.req:
            if action == DC {
                over = DC_C
            } else {
                over = update(&g, action)
            }
        // move by non-active player
        case action := <-g.oppPlay.req:
            if action == DC {
                over = DC_O
            } else {
                update(&g, action)
            }
        }
        // game ends
        if over != 0 {
            break
        }
    }

    g.endGame(over)
}

func update(g *Game, bPress byte) byte {
    g.oppPlay.wr.write("button" + string(bPress + '0')) // send press to opponent

    if g.prog.val == 3 {
        // new round
        // change sequence
        g.prog.next = &LList{val: 3}
        g.prog.val = bPress
        g.prog = g.seq
        // other vars
        g.currPlay, g.oppPlay = g.oppPlay, g.currPlay // swap turns
        g.timer.Reset(time.Second)
    } else if g.prog.val == bPress {
        // correct
        g.timer.Reset(time.Second)
        g.prog = g.prog.next
    } else {
        // incorrect
        return INGAME_C
    }

    return 0
}

func (g Game) endGame(reason byte) {
    fmt.Println("ending game")
    switch reason {
        // wrong press or timer ran out
        case INGAME_C:
            g.currPlay.wr.write("lose")
            g.oppPlay.wr.write("win")
        // disconnects
        case DC_C:
            g.oppPlay.wr.write("win")
        case DC_O:
            g.currPlay.wr.write("win")
    }

    // closes websockets
    close(g.currPlay.req)
    close(g.oppPlay.req)
    g.currPlay.inGame = false
    g.oppPlay.inGame = false

    g.timer.Stop()
}

func reader(con *websocket.Conn) {
    // connection handler
    pl := &Player{&Writer{con: con}, make(chan byte), false}
    queue <- pl

    // loop until disconnect
    for {
        // new read message
        _, p, err := con.ReadMessage();

        // disconnect / error
        if(err != nil) {
            if (pl.inGame) {
                pl.req <- DC
            }
            return
        }

        switch string(p) {
        // ping check
        case "here":
            pl.wr.write("seen")
        // button press
        case "button0", "button1", "button2":
            if (pl.inGame) {
                pl.req <- p[6] - '0'
            }
        }
    }
}

func makeSocket(w http.ResponseWriter, r *http.Request) {
    // form ws connection
    ws, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        fmt.Println("socket didn't go in: ", err)
        return
    }
    fmt.Println("socket in")
    reader(ws)
}

func mainPage(w http.ResponseWriter, r *http.Request) {
    // give html file
    http.ServeFile(w, r, "game.html")
    fmt.Println("main page in")
}

func main() {
    fmt.Println("started")

    // creating handle functions
    http.HandleFunc("/", mainPage)
    http.HandleFunc("/ws", makeSocket)

    // queue manager
    go func() {
        for {
            go initGame(<-queue, <-queue)
        }
    }()

    http.ListenAndServe(":8000", nil) // check ip
}
