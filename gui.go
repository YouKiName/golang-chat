// gui.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"fyne.io/fyne"
	"fyne.io/fyne/app"
	"fyne.io/fyne/dialog"
	"fyne.io/fyne/layout"
	"fyne.io/fyne/widget"

	"github.com/graarh/golang-socketio"
	"github.com/graarh/golang-socketio/transport"
)

const WIDTH int = 1280
const HEIGHT int = 720

const DEFAULT_HOST = "localhost"
const DEFAULT_PORT = 3811

const GROUP_CHANNEL_TITLE = "MAIN"
const NOTES_CHANNEL_TITLE = "NOTES"

type ChatApplication struct {
	App                 fyne.App
	Window              fyne.Window
	LeftSideBar         *widget.Group
	MessagesList        *MessageList
	MessageListScroller *widget.ScrollContainer
	Client              *gosocketio.Client
	Connected           bool
	CurrentUser         User
	LoggedIn            bool
	CurrentChatId       int64
	ProfileInfo         *widget.Label
	Channels            []Channel
	ChannelsRadioGroup  *widget.RadioGroup
	// messagesStorage map[int][]SavedMessage
}

func (chatApp *ChatApplication) init() {
	chatApp.App = app.New()
	window := chatApp.App.NewWindow("Golang chat")
	window.Resize(fyne.NewSize(WIDTH, HEIGHT))

	window.SetContent(buildMainWindow(chatApp))
	window.SetMaster()

	chatApp.Window = window
	chatApp.CurrentChatId = 0 // main channel
	chatApp.Connected = false
	chatApp.LoggedIn = false
}

func (chatApp *ChatApplication) showWindow() {
	chatApp.Window.ShowAndRun()
}

func (chatApp *ChatApplication) startReconnectionTrying() {
	time.Sleep(10 * time.Second)
	for i := 0; i < 10; i++ {
		if i != 0 {
			d, _ := time.ParseDuration(fmt.Sprintf("%dm", i))
			time.Sleep(d)
		}
		host, port := getHostDataFromSettingsFile()
		fmt.Printf("Try reconnect to: %s:%d\n", host, port)
		if chatApp.connect(host, port, true) {
			dialog.ShowInformation("Success!", "Connection restored!", chatApp.Window)
			return
		} else {
			fmt.Printf("Unsuccess reconnect to: %s:%d\nNext try after %d minutes\n",
				host, port, i+1)
		}
	}
}

func (chatApp *ChatApplication) connect(host string, port int, isReconnect bool) bool {
	time.Sleep(1 * time.Second)
	client, err := gosocketio.Dial(
		gosocketio.GetUrl(host, port, false),
		transport.GetDefaultWebsocketTransport())

	if isError(err) {
		if !isReconnect {
			info := fmt.Sprintf("Can't connect to host \"%s:%d\"\n"+
				"Next try after: 10 sec\n"+
				"Description: %s\n", host, port, err.Error())

			chatApp.showError(info)
			go chatApp.startReconnectionTrying()
		}
		return false
	}

	err = client.On(gosocketio.OnDisconnection, func(h *gosocketio.Channel) {
		chatApp.showError("Disconnected!")
	})
	if isError(err) {
		chatApp.showError(err.Error())
		return false
	}

	err = client.On(gosocketio.OnConnection, func(h *gosocketio.Channel) {
		log.Println("Connected")
	})
	if isError(err) {
		chatApp.showError(err.Error())
		return false
	}

	chatApp.Client = client

	chatApp.Connected = true
	chatApp.Window.SetOnClosed(func() {
		chatApp.Client.Close()
	})

	chatApp.initClientCallbacks()
	return true
}

func (chatApp *ChatApplication) initClientCallbacks() {
	client := chatApp.Client
	client.On("/failed-login", func(h *gosocketio.Channel, errorData LoginError) {
		log.Println(errorData.Description)
		chatApp.showLoginDialog(errorData.Description)
		chatApp.LoggedIn = false
		chatApp.ProfileInfo.SetText("FAILED LOGIN")
	})
	client.On("/failed-registeration", func(h *gosocketio.Channel, errorData RegistrationError) {
		log.Println(errorData.Description)
		chatApp.showRegisterDialog(errorData.Description)
		chatApp.LoggedIn = false
		chatApp.ProfileInfo.SetText("FAILED LOGIN")
	})

	client.On("/login", func(h *gosocketio.Channel, user User) {
		log.Println("LOGIN")
		chatApp.CurrentUser = user
		chatApp.LoggedIn = true
		chatApp.ProfileInfo.SetText("WELCOME, " + user.Username)
		chatApp.LeftSideBar.Append(widget.NewLabel("Success Login: " + user.Username))

		chatApp.CurrentChatId = GROUP_CHAT_ID

		chatApp.clearMessagesList()
		chatApp.loadChannels()
		// have not to load messages. Main channel will be loaded after
		// auto selecting this channel in the right sidebar
	})

	client.On("/message", func(h *gosocketio.Channel, msg SavedMessage) {
		currentChatId := chatApp.CurrentChatId
		currentUserId := chatApp.CurrentUser.Id
		chatType := msg.getChatType()
		isMessageFromMe := msg.UserData.Id == chatApp.CurrentUser.Id
		isMessageToMe := msg.ChatId == chatApp.CurrentUser.Id
		// notes message: recipient and sender is same person
		isNotesMessage := isMessageFromMe && isMessageToMe

		if chatType == "group" && chatApp.CurrentChatId == GROUP_CHAT_ID {
			chatApp.addMessageToList(msg)
		}

		if chatType == "private" {
			if isNotesMessage && currentChatId == currentUserId {
				chatApp.addMessageToList(msg)
			} else if isMessageFromMe && currentChatId == msg.ChatId {
				chatApp.addMessageToList(msg)
			} else if isMessageToMe && currentChatId == msg.UserData.Id {
				chatApp.addMessageToList(msg)
			}
		}
	})

	client.On("/get-messages", func(h *gosocketio.Channel, messages []SavedMessage) {
		fmt.Printf("Got Messages count = %d\n", len(messages))
		chatApp.clearMessagesList()
		chatApp.MessagesList.setMessages(messages)
		chatApp.MessagesList.refresh()
		chatApp.MessageListScroller.ScrollToTop()
	})
	client.On("/get-channels", func(h *gosocketio.Channel, channels []Channel) {
		fmt.Printf("Got channels count = %d\n", len(channels))
		chatApp.Channels = channels
		chatApp.refreshChannelList()
		if chatApp.CurrentChatId == GROUP_CHAT_ID {
			chatApp.ChannelsRadioGroup.SetSelected(GROUP_CHANNEL_TITLE)
		}
	})
}

func (chatApp *ChatApplication) sendLoginData(username string, password string) {
	if chatApp.Connected {
		chatApp.Client.Emit("/login", LoginData{username, getPasswordHash(password)})
	} else {
		chatApp.showError("You are not connected to the server.")
	}
}

func (chatApp *ChatApplication) sendRegisterData(username string, password string) {
	if chatApp.Connected {
		chatApp.Client.Emit("/register", LoginData{username, getPasswordHash(password)})
	} else {
		chatApp.showError("You are not connected to the server.")
	}
}

func (chatApp *ChatApplication) sendMessage(text string) {
	user := chatApp.CurrentUser
	if chatApp.Connected && chatApp.LoggedIn {
		chatApp.Client.Emit("/message", Message{user, chatApp.CurrentChatId, text})
	} else if !chatApp.LoggedIn {
		chatApp.showError("You are not logged in.")
	} else {
		chatApp.showError("You are not connected to the server.")
	}
}

func (chatApp *ChatApplication) addMessageToList(msg SavedMessage) {
	chatApp.MessagesList.addMessage(msg)
	chatApp.MessagesList.refresh()

}

func (chatApp *ChatApplication) clearMessagesList() {
	chatApp.MessagesList.clear()
	chatApp.MessagesList.refresh()
}

func (chatApp *ChatApplication) showError(description string) {
	log.Println(description)
	dialog.ShowError(errors.New(description), chatApp.Window)
}

func (chatApp *ChatApplication) loadChannels() {
	log.Println("Load channels")
	chatApp.Client.Emit("/get-channels", ChannelsRequest{chatApp.CurrentUser})
}

func (chatApp *ChatApplication) refreshChannelList() {
	// Use this function only after replacing channels.
	// After appending (group.Append) this func will be called automatically
	var stringChannels []string
	stringChannels = append(stringChannels, GROUP_CHANNEL_TITLE, NOTES_CHANNEL_TITLE)
	for _, channel := range chatApp.Channels {
		stringChannels = append(stringChannels, channel.Title)
	}
	chatApp.ChannelsRadioGroup.Options = stringChannels
	chatApp.ChannelsRadioGroup.Refresh()
}

func (chatApp *ChatApplication) getChannelId(title string) int64 {
	for _, channel := range chatApp.Channels {
		if channel.Title == title {
			return channel.Id
		}
	}
	if title == NOTES_CHANNEL_TITLE {
		return chatApp.CurrentUser.Id
	}
	return GROUP_CHAT_ID // MAIN chat
}

func (chatApp *ChatApplication) loadMessages(chatId int64) {
	if !chatApp.Connected || !chatApp.LoggedIn {
		return
	}
	fmt.Printf("Load messages from chatId = %d\n", chatId)
	client := chatApp.Client
	client.Emit("/get-messages", MessagesRequest{chatId, chatApp.CurrentUser})
}

func (chatApp *ChatApplication) isChannelInList(channelId int64) bool {
	list := chatApp.Channels
	for _, c := range list {
		if c.Id == channelId {
			return true
		}
	}
	return false
}

func (chatApp *ChatApplication) openChannel(chatId int64) {
	chatApp.CurrentChatId = chatId
	chatApp.loadMessages(chatId)
}

func (chatApp *ChatApplication) openNotesChannel() {
	chatApp.openChannel(chatApp.CurrentUser.Id)
}

func (chatApp *ChatApplication) openChannelByUser(user UserPublicInfo) {
	channelsGroup := chatApp.ChannelsRadioGroup

	if user.Id == chatApp.CurrentUser.Id { // NOTES
		channelsGroup.SetSelected(NOTES_CHANNEL_TITLE)
		return
	}

	if !chatApp.isChannelInList(user.Id) {
		channelsGroup.Append(user.Username)
		chatApp.Channels = append(chatApp.Channels, Channel{user.Id, user.Username})
	}
	channelsGroup.SetSelected(user.Username)
}

// -------- BUILD WINDOW----------

func (chatApp *ChatApplication) showLoginDialog(title string) {
	inputUsername := widget.NewEntry()
	inputUsername.SetPlaceHolder("username")
	inputPassword := widget.NewPasswordEntry()
	inputPassword.SetPlaceHolder("password")

	loginBox := widget.NewVBox(inputUsername, inputPassword)
	loginBox.Resize(fyne.NewSize(400, 400))

	dialog.ShowCustomConfirm(title, "Ok", "Cancel", loginBox,
		func(result bool) {
			if result {
				chatApp.sendLoginData(inputUsername.Text, inputPassword.Text)
			}
		}, chatApp.Window)
}

func (chatApp *ChatApplication) showRegisterDialog(title string) {
	inputUsername := widget.NewEntry()
	inputUsername.SetPlaceHolder("username")
	inputPassword := widget.NewPasswordEntry()
	inputPassword.SetPlaceHolder("password")

	loginBox := widget.NewVBox(inputUsername, inputPassword)
	loginBox.Resize(fyne.NewSize(400, 400))

	dialog.ShowCustomConfirm(title, "Ok", "Cancel", loginBox,
		func(result bool) {
			if result {
				chatApp.sendRegisterData(inputUsername.Text, inputPassword.Text)
			}
		}, chatApp.Window)
}

func buildLeftSidebar(chatApp *ChatApplication) *widget.Group {
	login := widget.NewButton("Login", func() {
		if chatApp.Connected {
			chatApp.showLoginDialog("Login")
		} else {
			chatApp.showError("You are not connected to the server.")
		}
	})
	register := widget.NewButton("Register", func() {
		if chatApp.Connected {
			chatApp.showRegisterDialog("Register")
		} else {
			chatApp.showError("You are not connected to the server.")
		}
	})

	info := widget.NewLabel("")
	chatApp.ProfileInfo = info

	group := widget.NewGroup("Profile", login, register, info)
	group.Resize(fyne.NewSize(400, HEIGHT))
	return group
}

func buildCenter(chatApp *ChatApplication) *fyne.Container {
	messagesList := newMessageList(func(user UserPublicInfo) {
		chatApp.openChannelByUser(user)
	})
	chatApp.MessagesList = messagesList
	scroller := widget.NewScrollContainer(messagesList.getContainer())
	scroller.SetMinSize(fyne.NewSize(500, 500))
	chatApp.MessageListScroller = scroller

	input := widget.NewEntry()
	input.SetPlaceHolder("Your message")

	send := widget.NewButton("Send", func() {
		if input.Text != "" {
			chatApp.sendMessage(input.Text)
			input.SetText("")
		}
	})

	top := widget.NewGroup("Messenger", scroller)
	bottom := widget.NewHBox(input, send)

	layout := layout.NewBorderLayout(top, bottom, nil, nil)

	return fyne.NewContainerWithLayout(layout, top, bottom)
}

func buildRightSidebar(chatApp *ChatApplication) fyne.Widget {
	var stringChannels []string
	radioGroup := widget.NewRadioGroup(stringChannels, func(changed string) {
		fmt.Printf("Select channel = %s\n", changed)
		selectedChatId := chatApp.getChannelId(changed)
		chatApp.openChannel(selectedChatId)
	})
	radioGroup.Required = true
	chatApp.ChannelsRadioGroup = radioGroup
	return widget.NewGroup("Channels", radioGroup)
}

func buildMainWindow(chatApp *ChatApplication) *fyne.Container {
	leftSideBar := buildLeftSidebar(chatApp)
	chatApp.LeftSideBar = leftSideBar

	center := buildCenter(chatApp)

	rightSideBar := buildRightSidebar(chatApp)
	return fyne.NewContainerWithLayout(
		layout.NewBorderLayout(nil, nil, leftSideBar, rightSideBar),
		leftSideBar,
		center, rightSideBar)
}

// ------------------
type HostData struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func saveDefaultHostSettings() {
	defaultHostData := HostData{DEFAULT_HOST, DEFAULT_PORT}
	jsonByteData, err := json.Marshal(defaultHostData)
	if isError(err) {
		log.Println(err)
	}
	err = ioutil.WriteFile("settings.json", jsonByteData, 0644)
	if isError(err) {
		log.Println(err)
	}
}

func getHostDataFromSettingsFile() (string, int) {
	// returns (host, port)
	f, err := ioutil.ReadFile("settings.json")
	if isError(err) {
		saveDefaultHostSettings()
		return DEFAULT_HOST, DEFAULT_PORT
	}

	hostData := HostData{}
	err = json.Unmarshal([]byte(f), &hostData)
	if isError(err) {
		saveDefaultHostSettings()
		return DEFAULT_HOST, DEFAULT_PORT
	}

	return hostData.Host, hostData.Port
}

func main() {
	chatApp := ChatApplication{}
	chatApp.init()
	host, port := getHostDataFromSettingsFile()
	go chatApp.connect(host, port, false)
	chatApp.showWindow()
}
