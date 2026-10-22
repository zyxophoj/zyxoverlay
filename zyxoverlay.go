package main

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	_ "image/png"

	"gopkg.in/ini.v1"

	"github.com/gopxl/pixel/v2"
	"github.com/gopxl/pixel/v2/backends/opengl"

	"zyxoverlay/fight_club"
	"zyxoverlay/twitch"
	"zyxoverlay/utils"
	"zyxoverlay/video"
)

type Config struct {
	Mode         string
	ParasitePort int
	ChannelName  string
	ArenaWidth   float64
	ArenaHeight  float64
	Background   color.RGBA
}

func main() {
	messages := make(chan map[string]string)

	// default values
	cfg := Config{
		Mode:         "IRC",
		ParasitePort: 3000,
		ChannelName:  "",
		ArenaWidth:   1280,
		ArenaHeight:  480,
		Background:   color.RGBA{0, 0, 0, 0xFF},
	}
	ini_data, err := ini.Load("zyxoverlay.ini")
	if err != nil {
		fmt.Println("Failed to load ini file!!!", err)

	} else {
		sec := ini_data.Section("base")
		sec.MapTo(&cfg)

		// Special case for Background because it is a non-built-in type
		col, err := utils.Colour_from_RGBstring(sec.Key("Background").String())
		if err != nil {
			fmt.Println("Could not decipher Background:", err)
		} else {
			cfg.Background = col
		}
	}

	switch strings.ToUpper(cfg.Mode) {
	case "PARASITE":
		go func() {
			err := twitch.Run_from_browser_parasite(messages, cfg.ParasitePort)
			if err != nil {
				fmt.Println(err)
				os.Exit(-1)
			}
		}()

	case "IRC":
		irc := twitch.IRC{}
		err := irc.Run(messages, cfg.ChannelName)
		if err != nil {
			return
		}
		defer irc.KillAndWait()

	default:
		fmt.Println("Unrecognised mode!  This should be PARASITE or IRC")
	}

	// opengl demands the main thread
	opengl.Run(func() { run_overlay(messages, &cfg) })
}

// OvO = Overlay Object
// i.e. anything (except the background) that needs to be drawn onto the window
type OvO interface {
	Tick(seconds float64)
	Draw(target pixel.Target)
	IsExpired() bool
}

func run_overlay(messages chan map[string]string, cfg *Config) {
	win, err := opengl.NewWindow(opengl.WindowConfig{
		Title:  "Do not talk about fight club",
		Bounds: pixel.R(0, 0, cfg.ArenaWidth, cfg.ArenaHeight),
		VSync:  true,
	})
	if err != nil {
		panic(err)
	}
	defer win.Destroy()

	fc := fight_club.Make_fight_club(cfg.ArenaWidth, cfg.ArenaHeight)
	vm, err := video.Make_video_manager(cfg.ArenaWidth, cfg.ArenaHeight)
	if err != nil {
		// not fatal, but videos won't work
		fmt.Println("Error reading videos dir:", err)
	}

	old_time := time.Now()
	for !win.Closed() {
		new_time := time.Now()
		frame_seconds := new_time.Sub(old_time).Seconds()
		old_time = new_time

		vm.Tick(frame_seconds)
		fc.Tick(frame_seconds)

		select {
		case message := <-messages:
			name := message["username"]
			text := message["message-text"]

			fc.Handle_message(name, text)
			vm.Handle_message(name, text)

		default:
			// This section - between the win.Clear and the win.Update - must be the only place where drawing happens.
			win.Clear(cfg.Background)

			vm.Draw(win)
			fc.Draw(win)

			win.Update()
		}
	}
}
