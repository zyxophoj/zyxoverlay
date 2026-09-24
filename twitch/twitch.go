package twitch

// A selection of (so far, two) back ends for getting messages from Twitch

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Run_from_browser_parasite receives twitch chat updates from the browser parasite,
// and writes properly-formed messages to the "messages" channel
// It does not create the parasite.
// It does not return.
func Run_from_browser_parasite(messages chan map[string]string, parasite_port int) error {
	raw_messages := make(chan []byte)

	go func() {
		// You know the rules, and so do I
		rules := map[string]string{
			"message-username":  "username",
			"chat-message-text": "message-text",
		}
		for raw := range raw_messages {
			parsed := map[string]string{}
			json.Unmarshal(raw, &parsed)

			doc, _ := html.Parse(strings.NewReader(parsed["dump"]))

			out := map[string]string{}
			crawl := func(node *html.Node) {}
			crawl = func(node *html.Node) {
				for _, att := range node.Attr {
					for k, v := range rules {
						if att.Val == k {
							out[v] = node.FirstChild.Data
						}
					}
				}

				for child := node.FirstChild; child != nil; child = child.NextSibling {
					crawl(child)
				}
			}
			crawl(doc)

			messages <- out
		}
	}()

	// HTTP handler
	// We must also handle the CORS OPTIONS message because twitch.tv->localhost is cross-site
	// (even when the browser and the overlay are on the same machine)
	http.HandleFunc("/zyxoverlay", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "POST":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				fmt.Println(err)
				w.WriteHeader(400)
				return
			}

			defer req.Body.Close()

			raw_messages <- body
			fallthrough
		case "OPTIONS":
			w.Header()["Access-Control-Allow-Origin"] = []string{"https://www.twitch.tv"}
			//fmt.Println("Responding to", req.Method,"with",200)
			w.WriteHeader(200)

		default:
			// Whatever this is, we don't do it
			w.WriteHeader(405)
		}
	})

	// TODO: make this function cancellable
	return http.ListenAndServe(fmt.Sprintf(":%v", parasite_port), nil)
}

// IRC is used for receiving messages from Twitch's IRC interface
// There is no constructor-type function; just default-conbstruct an IRC and call Run
type IRC struct {
	kill chan bool
	dead chan bool
}

// Run connects to IRC and writes properly-formed messages to the "messages" channel
// It tries to reconnect if the connection is lost.
// If it returns nil, a listener is started - this can be stopped with KillAndWait.
// If it returns an error, no cleanup is needed.
func (i *IRC) Run(messages chan map[string]string, channel_name string) error {
	if strings.TrimSpace(channel_name) == "" {
		fmt.Println("IRC mode requires a non-empty ChannelName in the config")
		// (and Twitch does not; I found that out the hard way.)
		return errors.New("IRC mode requires a non-empty ChannelName in the config")
	}
	const (
		server = "irc.chat.twitch.tv:6697"
		// 6697 is TLS; 6667 is plain
		// As of August 2025, plain is no longer an option:
		// https://discuss.dev.twitch.com/t/decommission-of-non-secure-websocket-connections-to-twitch-irc-servers/64142
	)

	i.kill = make(chan bool)
	i.dead = make(chan bool)

	go func() {
		for {
			fmt.Println("Connecting...")
			// Nicks starting with "justinfan" are magic - Twitch allows them to log in with "oauth:anonymous".
			// They can not write to chat when they do this, but we only need to read. (at the current state of development, anyway)
			err := i.connectAndRead(server, fmt.Sprintf("justinfan%d", rand.Intn(1<<20)), channel_name, messages)
			select {
			case <-i.kill:
				fmt.Println("Killed by caller; Run returning")
				close(i.dead)
				return
			default:
			}

			// If we're shouldn't be dead then try to recover
			fmt.Println("IRC disconnected: %v — reconnecting in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}()

	return nil
}

// KillAndWait politely asks everything started by IRC.Run to stop and waits for that to happen.
// This will attempt to leave the IRC channel
func (i *IRC) KillAndWait() {
	close(i.kill)
	<-i.dead
}

func (i *IRC) connectAndRead(server, nick, channel string, messages chan<- map[string]string) error {
	// TLS connection (preferred)
	conn, err := tls.Dial("tcp", server, &tls.Config{})
	if err != nil {
		return err
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	send := func(line string) error {
		_, err := w.WriteString(line + "\r\n")
		if err != nil {
			return err
		}
		return w.Flush()
	}

	for _, magic := range []string{
		"CAP REQ :twitch.tv/tags twitch.tv/commands",
		"PASS oauth:anonymous",
		"NICK " + nick,
		"JOIN #" + strings.ToLower(channel),
	} {
		if err := send(magic); err != nil {
			return err
		}
	}
	fmt.Println("Joined", channel, "as", nick)

	raw_messages := make(chan string)
	// read loop
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				fmt.Println("READ ERROR", err)
				return
			}
			raw_messages <- line
		}
	}()

	for {
		select {
		case <-i.kill:
			// The attempt to leave should be made, since it's rude to not clean up the mess we
			// made in someone else's house.
			// We don't really care about the error here.  If we can't send then we're probably
			// kicked anyway, and in any case there's nothing we can do about it beyond logging.
			err := send("PART #" + channel)
			fmt.Println("PARTed from #"+channel+" - error =", err)
			return nil

		case line := <-raw_messages:
			line = strings.TrimRight(line, "\r\n")
			fmt.Println("MESSAGE: [", line, "]")

			// Keepalive
			if strings.HasPrefix(line, "PING ") {
				pong := "PONG " + strings.TrimPrefix(line, "PING ")
				if err := send(pong); err != nil {
					return err
				}
				continue
			}

			// PRIVMSG example (with tags):
			// @badge-info=...;display-name=SomeUser;... :someuser!someuser@someuser.tmi.twitch.tv PRIVMSG #channel :hello world
			if !strings.Contains(line, " PRIVMSG #") {
				continue
			}

			username, text := parsePrivmsg(line)
			if username == "" || text == "" {
				continue
			}

			messages <- map[string]string{
				"username":     username,
				"message-text": text,
			}
		}
	}
}

// Very small parser that extracts display-name (or login) and the message text.
func parsePrivmsg(line string) (username, text string) {
	// Split tags from the rest
	var tagsPart, rest string
	if strings.HasPrefix(line, "@") {
		idx := strings.Index(line, " ")
		if idx == -1 {
			return "", ""
		}
		tagsPart = line[1:idx]
		rest = line[idx+1:]
	} else {
		rest = line
	}

	// Prefer display-name from tags
	for _, tag := range strings.Split(tagsPart, ";") {
		if strings.HasPrefix(tag, "display-name=") {
			username = tag[len("display-name="):]
			break
		}
	}

	// Fallback: extract login from the prefix (:user!user@...)
	if username == "" {
		if strings.HasPrefix(rest, ":") {
			bang := strings.Index(rest, "!")
			if bang > 1 {
				username = rest[1:bang]
			}
		}
	}

	// Message text is everything after the second " :"
	msgIdx := strings.Index(rest, " :")
	if msgIdx == -1 {
		return "", ""
	}
	text = rest[msgIdx+2:]

	return username, text
}
