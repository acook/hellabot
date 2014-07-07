package hbot

import (
	"fmt"
	"net"
	"time"
	"bufio"
	"bitbucket.org/madmo/sendfd"
)

type IrcCon struct {
	con net.Conn
	Incoming chan *Message
	outgoing chan string
	tr []*Trigger
	Channels map[string]*IrcChannel

	//this bots nick
	nick string

	//the unix domain socket address for reconnects
	unixastr string

	//Whether or not this is a reconnect instance
	reconnect bool
}

//Connect to an irc server
func NewIrcConnection(host, nick string) *IrcCon {
	irc := new(IrcCon)

	//Attempt reconnection
	if !irc.HijackSession() {
		var err error
		irc.con,err = net.Dial("tcp", host)
		if err != nil {
			panic(err)
		}
	}

	irc.Incoming = make(chan *Message, 16)
	irc.outgoing = make(chan string, 16)
	irc.Channels = make(map[string]*IrcChannel)
	irc.nick = nick
	irc.unixastr = fmt.Sprintf("@%s/irc", nick)

	irc.AddTrigger(pingPong)
	return irc
}

//Incoming message gathering routine
func (irc *IrcCon) handleIncomingMessages() {
	scan := bufio.NewScanner(irc.con)
	for scan.Scan() {
		mes := ParseMessage(scan.Text())
		consumed := false
		for _,t := range irc.tr {
			if t.Condition(mes) {
				consumed = t.Action(irc,mes)
			}
			if consumed {
				break
			}
		}
		if !consumed {
			irc.Incoming <- mes
		}
	}
}

//handles message speed throtling
func (irc *IrcCon) handleOutgoingMessages() {
	for s := range irc.outgoing {
		_,err := fmt.Fprint(irc.con, s + "\r\n")
		if err != nil {
			panic(err)
		}
		time.Sleep(time.Millisecond * 200)
	}
}

//Attempt to hijack session previously running bot
func (irc *IrcCon) HijackSession() bool {
	unaddr,err := net.ResolveUnixAddr("unix", irc.unixastr)
	if err != nil {
		panic(err)
	}

	con,err := net.DialUnix("unix", nil, unaddr)
	if err != nil {
		fmt.Println("Couldnt restablish connection, no prior bot.")
		fmt.Println(err)
		return false
	}

	ncon,err := sendfd.RecvFD(con)
	if err != nil {
		panic(err)
	}

	netcon,err := net.FileConn(ncon)
	if err != nil {
		panic(err)
	}

	irc.reconnect = true
	irc.con = netcon
	return true
}

//Start up servers various running methods
func (irc *IrcCon) Start() {
	go irc.handleIncomingMessages()
	go irc.handleOutgoingMessages()

	go func() {
		unaddr,err := net.ResolveUnixAddr("unix", fmt.Sprintf("@%s/irc", irc.nick))
		if err != nil {
			panic(err)
		}
		list,err := net.ListenUnix("unix", unaddr)
		if err != nil {
			panic(err)
		}
		con,err := list.AcceptUnix()
		if err != nil {
			panic(err)
		}
		list.Close()

		fi,err := irc.con.(*net.TCPConn).File()
		if err != nil {
			panic(err)
		}

		err = sendfd.SendFD(con,fi)
		if err != nil {
			panic(err)
		}

		close(irc.Incoming)
		close(irc.outgoing)
	}()

	//Only register on an initial connection
	if !irc.reconnect {
		//Server registration
		irc.Send(fmt.Sprintf("USER %s 8 * :%s", irc.nick, irc.nick))
		irc.Send(fmt.Sprintf("NICK %s", irc.nick))
	}
}

//Send a message to 'who' (user or channel)
func (irc *IrcCon) Msg(who, text string) {
	irc.Send("PRIVMSG " + who + " :" + text)
}

//Send any command to the server
func (irc *IrcCon) Send(command string) {
	irc.outgoing <- command
}

//Used to change users modes in a channel
//operator = "+o" deop = "-o"
//ban = "+b"
func (irc *IrcCon) ChMode(user, channel, mode string) {
	irc.Send("MODE " + channel + " " + mode + " " + user)
}

//Join a channel and register its struct in the IrcCons channel map
func (irc *IrcCon) Join(ch string) *IrcChannel {
	irc.Send("JOIN " + ch)
	ichan := &IrcChannel{Name: ch, con: irc, Counts: make(map[string]int)}

	irc.Channels[ch] = ichan
	ichan.TryLoadStats(ch[1:] + ".stats")
	return ichan
}

func (irc *IrcCon) AddTrigger(t *Trigger) {
	irc.tr = append(irc.tr, t)
}

//A struct used to subscribe and react to events on the Irc Server
type Trigger struct {
	//Returns true if this trigger applies to the passed in message
	Condition func (*Message) bool

	//The action to perform if Condition is true
	//return true if the message was 'consumed'
	Action func (*IrcCon,*Message) bool
}

//A trigger to respond to the servers ping pong messages
//If PingPong messages are not responded to, the server assumes the
//client has timed out and will close the connection.
//Note: this is automatically added in the IrcCon constructor
var pingPong = &Trigger{
	func (m *Message) bool {
		return m.Content == "PING"
	},
	func (irc *IrcCon, m *Message) bool {
		irc.Send("PONG :" + m.Content)
		return true
	},
}
