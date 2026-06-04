package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/net/html"
	"image"
	"image/color"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	_ "image/png"

	"golang.org/x/image/font/basicfont"
	"gopkg.in/ini.v1"

	"github.com/gopxl/pixel/v2"
	"github.com/gopxl/pixel/v2/backends/opengl"
	"github.com/gopxl/pixel/v2/ext/text"

	"zyxoverlay/utils"
)

func main() {
	raw_messages := make(chan []byte)

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

	messages := make(chan map[string]string)

	// Receive twitch chat updates from browser parasite,
	// extract username and message.
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

	go func() {
		err := http.ListenAndServe(":80", nil)
		if err != nil {
			fmt.Println(err)
			os.Exit(-1)
		}
	}()

	// opengl demands the main thread
	opengl.Run(func() { run_fight_club(messages) })
}

// Fight club stuff starts here
type Config struct {
	TextHeight         float64
	ArenaWidth         float64
	ArenaHeight        float64
	PushHeight         float64
	PushSpeed          float64
	MaxActivePlatforms int
	Gravity            float64
	DudeWidth          float64
	DudeHeight         float64
	PlatformPadding    float64
	TeabagTime         float64
	Atlas              *text.Atlas
}

type Colours struct {
	Background    color.RGBA
	NameText      color.RGBA
	PlatformText  color.RGBA
	HitpointsText color.RGBA
	Platform      color.RGBA
}

type fight_club_globals struct {
	cfg     Config
	colours Colours
	atlas   *text.Atlas
	sprites map[string]*pixel.Sprite
}

var FIGHT_CLUB_GLOBALS = &fight_club_globals{}

func get_config() (*Config, *Colours, *text.Atlas) {
	if FIGHT_CLUB_GLOBALS.atlas == nil {

		FIGHT_CLUB_GLOBALS.cfg = Config{
			TextHeight:         13,
			ArenaWidth:         1280,
			ArenaHeight:        480,
			PushHeight:         75,
			PushSpeed:          15,
			MaxActivePlatforms: 5,
			Gravity:            -250.0, // up is positive.  Since dudes are 50 pixels high, this is roughly equivalent to standard 9.8ms^-2.
			DudeWidth:          20,
			DudeHeight:         50,
			PlatformPadding:    5.0,
			TeabagTime:         0.8,
		}

		FIGHT_CLUB_GLOBALS.colours = Colours{
			Background:    color.RGBA{0, 0, 0, 0xFF},
			PlatformText:  color.RGBA{0xFF, 0xFF, 0xFF, 0xFF},
			NameText:      color.RGBA{0xFF, 0, 0, 0xFF},
			HitpointsText: color.RGBA{0x80, 0, 0, 0xFF},
			Platform:      color.RGBA{0, 0, 0x80, 0xFF},
		}

		ini_data, err := ini.Load("zyxoverlay.ini")
		if err != nil {
			fmt.Println("Failed to load ini file!!!", err)
		} else {
			sec := ini_data.Section("constants")
			sec.MapTo(&FIGHT_CLUB_GLOBALS.cfg)

			sec = ini_data.Section("colours")
			// ini.MapTo fails with no error, so we don't use it here
			rc := reflect.ValueOf(&FIGHT_CLUB_GLOBALS.colours).Elem() //reflect pointer and follow it in reflectland to make things addressible
			for i := 0; i < rc.NumField(); i++ {
				name := rc.Type().Field(i).Name
				if sec.HasKey(name) {
					col, err := utils.Colour_from_RGBstring(sec.Key(name).String())
					if err != nil {
						fmt.Println("Could not decipher ", name, err)
						continue
					}

					*(rc.Field(i).Addr().Interface().(*color.RGBA)) = col
				}
			}
		}

		// TODO: try to pick a font based on TextHeight
		FIGHT_CLUB_GLOBALS.atlas = text.NewAtlas(basicfont.Face7x13, text.ASCII)
	}

	return &FIGHT_CLUB_GLOBALS.cfg, &FIGHT_CLUB_GLOBALS.colours, FIGHT_CLUB_GLOBALS.atlas
}

type sprite int

const (
	Dude sprite = iota
	DudeTeabag
	Corpse
)

// get_sprite gets a sprite
// only the first call loads a file; subsequent calls use a cached
// The type-safety here is a bit illusory since any int literal can be auto-converted to sprite.
func get_sprite(name sprite) *pixel.Sprite {
	filename := "images/" + map[sprite]string{
		Dude:       "dude",
		DudeTeabag: "dudeteabag",
		Corpse:     "corpse",
	}[name] + ".png"
	current := FIGHT_CLUB_GLOBALS.sprites[filename]
	if current != nil {
		return current
	}

	file, err := os.Open(filename)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	dude_pic := pixel.PictureDataFromImage(img)
	out := pixel.NewSprite(dude_pic, dude_pic.Bounds())

	if FIGHT_CLUB_GLOBALS.sprites[filename] == nil {
		FIGHT_CLUB_GLOBALS.sprites = map[string]*pixel.Sprite{}
	}
	FIGHT_CLUB_GLOBALS.sprites[filename] = out
	return out
}

func solid_rect_sprite(rect pixel.Rect, colour color.RGBA) *pixel.Sprite {
	pd := pixel.MakePictureData(rect)
	for i := range pd.Pix {
		pd.Pix[i] = colour
	}
	return pixel.NewSprite(pd, pd.Bounds())
}

type FCO interface {
	Tick(seconds float64)
	Draw(target pixel.Target)
}

type platform struct {
	sprite *pixel.Sprite
	text   *text.Text

	rect pixel.Rect

	// These are relative to top left of rect
	sprite_offset pixel.Vec
	text_offset   pixel.Vec

	age float64
}

func make_platform(message string) *platform {
	cfg, colours, atlas := get_config()

	plat_text := text.New(pixel.V(0, 0), atlas)
	width := plat_text.BoundsOf(message).W()
	if width > cfg.ArenaWidth*0.6 {
		message = "[WALL Of TEXT]"
		width = plat_text.BoundsOf(message).W()
	}
	plat_text.Color = colours.PlatformText
	fmt.Fprintln(plat_text, message)
	plat_width := width + 2*5
	plat_height := cfg.TextHeight + 2*1
	plat_sprite := solid_rect_sprite(pixel.Rect{pixel.V(0, 0), pixel.V(plat_width, plat_height)}, colours.Platform)

	left := rand.Float64() * (cfg.ArenaWidth - plat_width)
	left = float64(int(left))

	return &platform{plat_sprite, plat_text,
		pixel.R(left, -plat_height, left+plat_width, 0),
		pixel.Vec{X: plat_width / 2, Y: plat_height / 2}, // sprites are drawn based on centre
		pixel.Vec{X: 5, Y: 1 + 2},                        // but text is drawn based off top-left corner!!  And that "2" make no sense,
		0,
	}
}

func (p *platform) Move(dx float64, dy float64) {
	p.rect = p.rect.Moved(pixel.V(dx, dy))
}

func (p *platform) Draw(target pixel.Target) {
	top_left := pixel.V(p.rect.Min.X, p.rect.Min.Y)

	p.sprite.Draw(target, pixel.IM.Moved(p.sprite_offset.Add(top_left)))
	p.text.Draw(target, pixel.IM.Moved(p.text_offset.Add(top_left)))
}

type dude struct {
	name           string
	name_text      *text.Text
	hitpoints_text *text.Text
	width, height  int
	x, y           float64 //bottom centre
	dx, dy         float64

	name_offset pixel.Vec

	hitpoints        float64
	cooldown_seconds float64

	teabag_cooldown float64
	teabagee        *corpse

	// TODO: mode?
}

func make_dude(name string, arena_width float64, arena_height float64) *dude {
	_, colour, atlas := get_config()

	dude_text := text.New(pixel.V(0, 0), atlas)
	dude_text.Color = colour.NameText
	fmt.Fprintln(dude_text, name)

	dude_width := 20

	hp_text := text.New(pixel.V(0, 0), atlas)
	hp_text.Color = colour.HitpointsText
	fmt.Fprintln(hp_text, "99")

	return &dude{name, dude_text, hp_text,
		dude_width, 50,
		float64(dude_width)/2.0 + rand.Float64()*(arena_width-float64(dude_width)), arena_height,
		0, 0,
		pixel.V(-0.5*dude_text.BoundsOf(name).W(), 50),
		99, 0, 0, nil,
	}
}

func (d *dude) Tick(seconds float64) {
	cfg, _, _ := get_config()

	d.dy += cfg.Gravity * seconds
	if (d.x < 0 && d.dx < 0) || (d.x > cfg.ArenaWidth && d.dx > 0) {
		d.dx = -0.9 * d.dx
	}

	d.x += d.dx * seconds
	d.y += d.dy * seconds

	d.teabag_cooldown -= seconds
	if d.teabag_cooldown < 0 {
		d.teabag_cooldown = 0
	}

	if d.dx == 0 && d.teabag_cooldown == 0 {
		d.dx = 150 * (rand.Float64() - 0.5)
	}
}

func (d *dude) Draw(target pixel.Target) {
	cfg, _, _ := get_config()

	position := pixel.V(d.x, d.y)
	sprite := get_sprite(Dude)
	if d.teabag_cooldown > 0 {
		sprite = []*pixel.Sprite{sprite, get_sprite(DudeTeabag)}[int(5.0*d.teabag_cooldown/cfg.TeabagTime)%2]
	}
	height := sprite.Frame().H()

	d.name_text.Draw(target, pixel.IM.Moved(pixel.V(d.name_offset.X, height).Add(position)))
	sprite.Draw(target, pixel.IM.Moved(position.Add(pixel.V(0, height/2))))
	d.hitpoints_text.Draw(target, pixel.IM.Moved(position.Add(pixel.V(
		-0.5*d.hitpoints_text.BoundsOf(strconv.Itoa(int(d.hitpoints))).W(), height-15))))
}

// update_hp increases a dude's HP by the specified amount (which can be negative)
// It also updates cached drawing data that depends on HP.
// This may create a corpse, which is simply returned (it's up to the caller to add the corpse to the world)
func (d *dude) update_hp(change float64) *corpse {
	old_hitpoints := d.hitpoints
	d.hitpoints += change
	d.hitpoints_text.Clear()
	fmt.Fprintln(d.hitpoints_text, strconv.Itoa(int(d.hitpoints)))

	if d.hitpoints < 0 && !(old_hitpoints < 0) {
		return make_corpse(d.name, d.x, d.y, d.dx, d.dy)
	}

	return nil
}

type corpse struct {
	name        string
	name_text   *text.Text
	name_offset pixel.Vec

	x, y   float64 //bottom centre
	dx, dy float64
}

func make_corpse(name string, x float64, y float64, dx float64, dy float64) *corpse {
	_, colour, atlas := get_config()

	name_text := text.New(pixel.V(0, 0), atlas)
	name_text.Color = colour.NameText
	fmt.Fprintln(name_text, name)

	return &corpse{name, name_text, pixel.V(-0.5*name_text.BoundsOf(name).W(), 30), x, y, dx, dy}
}

func (c *corpse) Draw(target pixel.Target) {
	position := pixel.V(c.x, c.y)

	c.name_text.Draw(target, pixel.IM.Moved(c.name_offset.Add(position)))
	get_sprite(Corpse).Draw(target, pixel.IM.Moved(position.Add(pixel.V(0, 10))))
}

func (c *corpse) Tick(seconds float64) {
	cfg, _, _ := get_config()

	c.dy += cfg.Gravity * seconds

	c.x += c.dx * seconds
	c.y += c.dy * seconds

	if (c.x < 0 && c.dx < 0) || (c.x > cfg.ArenaWidth && c.dx > 0) {
		c.dx = -0.9 * c.dx
	}

	// Corpses can not walk, so there is drag.
	// TODO: this should really only apply when the corpse is on a platform or on the ground
	c.dx *= math.Exp(-seconds * 0.5)
}

// run_fight_club does something we don't talk about
func run_fight_club(messages chan map[string]string) {
	const TEXT_HEIGHT = 13 // because hard-coded basicfont.Face7x13.

	last_user := "sdfhjasldfhal"
	last_message := "sjklfhasjkld2"

	cfg, colour, _ := get_config()

	wcfg := opengl.WindowConfig{
		Title:  "Do not talk about fight club",
		Bounds: pixel.R(0, 0, cfg.ArenaWidth, cfg.ArenaHeight),
		VSync:  true,
	}
	win, err := opengl.NewWindow(wcfg)
	if err != nil {
		panic(err)
	}
	defer win.Destroy()

	queued_platforms := []*platform{} // oldest first
	active_platforms := []*platform{}
	dudes := map[string]*dude{}
	corpses := map[*corpse]bool{}

	pushing := false
	push_height := 0.0

	old_time := time.Now()
	for !win.Closed() {
		new_time := time.Now()
		tick := new_time.Sub(old_time)
		old_time = new_time

		// Platforms time out after a while
		if len(active_platforms) > 0 {
			active_platforms[0].age += tick.Seconds()
			if active_platforms[0].age > 60 {
				active_platforms = active_platforms[1:]
			}
		}

		// New platforms wake up pushing
		if !pushing && len(queued_platforms) > 0 {
			active_platforms = append(active_platforms, queued_platforms[0])
			for len(active_platforms) > 5 {
				active_platforms = active_platforms[1:]
			}
			queued_platforms = queued_platforms[1:]

			push_height = 0.0
			pushing = true
		}

		if pushing {
			// To reduce backlog, long queues increase pushing speed
			push_change := tick.Seconds() * cfg.PushSpeed * float64(1+len(queued_platforms))
			push_height += push_change
			if push_height > cfg.PushHeight {
				push_change -= (push_height - cfg.PushHeight)
				push_height = cfg.PushHeight
				pushing = false
			}

			for _, p := range active_platforms {
				p.Move(0, push_change)
			}
		}

		// Fight!
		// TODO: parachuting dudes can't fight or be fought
		// TODO: dudes in cooldown can't fight

		// To avoid rug-pulls, we'll record who should be removed from the game due to deadness here,
		// and remove them after the fight loop.
		morgue := []*dude{}

		for _, d1 := range dudes {
			for _, d2 := range dudes {
				if d1 == d2 {
					continue
				}

				// Must be close enough to fight
				if math.Abs(d1.x-d2.x) > 7 || math.Abs(d1.y-d2.y) > 1 {
					continue
				}

				// Rule 3:  If somebody dies, the fight is over (for them, at least)
				if d1.hitpoints < 0 || d2.hitpoints < 0 {
					continue
				}

				xdiff := d2.x - d1.x
				dxdiff := d2.dx - d1.dx
				if dxdiff*xdiff >= 0 {
					//They are moving apart
					continue
				}

				if d1.dx*d2.dx <= 0 {
					//Moving towards each other
					d1.dx, d2.dx = -0.9*d1.dx, -0.9*d2.dx
					d1.dy += 20
					d2.dy += 20
					// update_hp last so that corpses properly inherit position and velocity
					damage1, damage2 := math.Sqrt(d1.hitpoints), math.Sqrt(d2.hitpoints)
					corpses[d1.update_hp(-damage2)] = true
					corpses[d2.update_hp(-damage1)] = true

					fmt.Println(d1.name, "hits", d2.name, "down to", d2.hitpoints)
					fmt.Println(d2.name, "hits", d1.name, "down to", d1.hitpoints)
				} else {
					//Backstab!
					stabber, victim := d1, d2
					if math.Abs(d2.dx) > math.Abs(d1.dx) {
						stabber, victim = d2, d1
					}

					// There is an element of cartoon physics here, but there is also an important
					// balance consideration.  Dudes get backstabbed when they are walking too slowly.
					// We don't want a permanently-slow-moving (and therefore, -backstab-receiving)
					// subclass of dude, so a backstabbee gets a generous "donation" of speed.
					ddx := (stabber.dx - victim.dx)
					victim.dx += 3.0 * ddx
					victim.dy += math.Abs(ddx)
					corpses[victim.update_hp(-2*math.Sqrt(stabber.hitpoints))] = true // Double damage!

					fmt.Println(stabber.name, "backstabs", victim.name, "down to", victim.hitpoints)
				}

				if d1.hitpoints < 0 {
					morgue = append(morgue, d1)
				}
				if d2.hitpoints < 0 {
					morgue = append(morgue, d2)
				}
			}
		}
		for _, d := range morgue {
			delete(dudes, d.name)
		}

		for _, d := range dudes {
			old_d_y := d.y
			d.Tick(tick.Seconds())

			// collision with ground
			if d.y < 0 {
				d.y = 0
				d.dy = 0 // Todo: bounce?
			}

			// collision with platforms
			for _, plat := range active_platforms {
				if plat.rect.Min.X < d.x && d.x < plat.rect.Max.X &&
					d.y < plat.rect.Max.Y && old_d_y > plat.rect.Min.Y {
					d.dy = 0
					d.y = plat.rect.Max.Y
				}
			}
		}

		delete(corpses, nil)

		for d := range corpses {
			old_d_y := d.y
			d.Tick(tick.Seconds())

			// collision with ground
			if d.y < 0 {
				d.y = 0
				if d.dy < 0 {
					d.dy = -0.4 * d.dy
				}
			}

			// collision with platforms
			for _, plat := range active_platforms {
				if plat.rect.Min.X < d.x && d.x < plat.rect.Max.X &&
					d.y < plat.rect.Max.Y && old_d_y > plat.rect.Min.Y {
					if d.dy < 0 {
						d.dy = -0.4 * d.dy
					} else {
						d.dy = 0
					}
					d.y = plat.rect.Max.Y
				}
			}
		}

		// Dude-corpse interaction: Teabagging!
		for c, _ := range corpses {
			for _, d := range dudes {
				if d.teabagee != nil {
					// already teabagging
					continue
				}

				//Close enough?
				if math.Abs(d.x-c.x) > 7 || math.Abs(d.y-c.y) > 1 {
					continue
				}

				d.teabag_cooldown = cfg.TeabagTime
				d.teabagee = c
				d.dx = 0
				c.dx = 0
				c.dy = 0

				// Note: once teabagging starts, we let it finish even if the teabager gets launched into orbit.
				// This is considered to be a feature, because it is funny.
			}
		}

		for _, d := range dudes {
			if d.teabagee != nil && d.teabag_cooldown == 0 {
				d.update_hp(+20)
				//d.dx = 150 * (rand.Float64() - 0.5)

				delete(corpses, d.teabagee)
				d.teabagee = nil
			}
		}

		select {
		case message := <-messages:
			name := message["username"]
			if name == "" || (name == last_user && message["message-text"] == last_message) {
				continue
			}
			last_user = name
			last_message = message["message-text"]

			text := name + ": " + message["message-text"]
			queued_platforms = append(queued_platforms, make_platform(text))

			if dudes[name] == nil {
				dudes[name] = make_dude(name, cfg.ArenaWidth, cfg.ArenaHeight)
			}

		default:

			// DRAWING STARTS HERE
			win.Clear(colour.Background)

			for _, plat := range active_platforms {
				plat.Draw(win)
			}
			for _, d := range dudes {
				d.Draw(win)
			}
			for c := range corpses {
				c.Draw(win)
			}
			win.Update()
			// DRAWING ENDS HERE
		}
	}

}
