package video

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math/rand"
	"strings"
	"time"

	"os"
	"os/exec"

	ffmpeg "github.com/u2takey/ffmpeg-go"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/pixel/v2"
)

type Videos struct {
	names  map[string]string
	width  float64
	height float64
	videos map[*video]bool
}

func Make_video_manager(width float64, height float64) (*Videos, error) {
	sampleRate := beep.SampleRate(44100)
	if err := speaker.Init(sampleRate, sampleRate.N(time.Second/10)); err != nil {
		return nil, err
	}

	dir, err := os.Open("videos")
	if err != nil {
		return nil, err
	}
	filenames, err := dir.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	v := Videos{map[string]string{}, width, height, map[*video]bool{}}
	for _, name := range filenames {
		v.names[strings.Split(name, ".")[0]] = "videos/" + name
	}
	return &v, nil
}

func (v *Videos) Handle_message(name string, text string) {

	if strings.HasPrefix(text, "!") {
		command := text[1:]
		filename, ok := v.names[command]
		if ok {
			vid, err := make_video(filename, v.width, v.height)
			if err != nil {
				fmt.Println("Error loading video:", err)
			} else {
				v.videos[vid] = true
			}
		}
	}
}

func (v *Videos) Tick(seconds float64) {
	morgue := []*video{} // avoid delete-during-iteration rug-pulls

	for vid := range v.videos {
		vid.Tick(seconds)
		if vid.IsExpired() {
			morgue = append(morgue, vid)
		}
	}
	for _, vid := range morgue {
		fmt.Println("Deleting expired video")
		delete(v.videos, vid)
	}
}

func (v *Videos) Draw(target pixel.Target) {
	for vid := range v.videos {
		vid.Draw(target)
	}
}

type video struct {
	rect pixel.Rect

	vid_reader          io.Reader
	finished_video      chan error
	finished_video_flag bool
	frame_time          float64
	time_since_last     float64
	video_buffer        []byte

	finished_audio      chan error
	finished_audio_flag bool
}

type streamInfo struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// ffmpegStreamer implements beep.Streamer by reading s16le PCM from ffmpeg's stdout.
type ffmpegStreamer struct {
	reader   io.Reader
	channels int
	err      error
}

func (f *ffmpegStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if f.err != nil {
		return 0, false
	}

	for i := range samples {
		// Read one sample per channel (2 bytes each for s16le)
		var buf [4]byte // 2 channels × 2 bytes
		_, err := io.ReadFull(f.reader, buf[:])
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				f.err = err
				return i, false
			}
			f.err = err
			return i, false
		}

		// Convert s16le → float64 in [-1.0, 1.0]
		left := int16(binary.LittleEndian.Uint16(buf[0:2]))
		right := int16(binary.LittleEndian.Uint16(buf[2:4]))
		samples[i][0] = float64(left) / 32768.0
		samples[i][1] = float64(right) / 32768.0
	}

	return len(samples), true
}

func (f *ffmpegStreamer) Err() error {
	return f.err
}

func make_video(filename string, arenaWidth float64, arenaHeight float64) (*video, error) {
	full_path := filename
	out, err := ffmpeg.Probe(full_path)
	if err != nil {
		return nil, err
	}
	var info streamInfo
	err = json.Unmarshal([]byte(out), &info)
	if err != nil {
		return nil, err
	}

	videocount := 0
	w, h := 0, 0
	frame_time := 0.0
	for _, s := range info.Streams {
		if s.CodecType == "video" {
			videocount += 1
			w, h = s.Width, s.Height

			var num, den float64
			_, err := fmt.Sscanf(s.AvgFrameRate, "%f/%f", &num, &den)
			if err != nil {
				// TODO: wrap
				return nil, err
			}
			if num*den == 0 {
				// this really should not happen
				return nil, errors.New("ffprobe returned bogus frame rate")
			}
			frame_time = den / num
		}
	}
	if videocount != 1 {
		// How do you expect me to play that?
		return nil, fmt.Errorf("File %v contains %v (which is not 1) video streams", filename, videocount)
	}
	// "f" versions of variables because pixel is float-based, but keep the originals since we absolutely do not want
	// to make an array of fw*fh*4 bytes.
	fw, fh := float64(w), float64(h)

	if float64(w) > arenaWidth || float64(h) > arenaHeight {
		return nil, errors.New("Video won't fit in the window")
	}

	x, y := rand.Intn(int(arenaWidth)-w), rand.Intn(int(arenaHeight)-h)
	fx, fy := float64(x), float64(y)

	frames_r, frames_w := io.Pipe()
	finished_video := make(chan error)
	go func() {
		finished_video <- ffmpeg.Input(full_path).Output("pipe:", ffmpeg.KwArgs{
			"format":  "rawvideo",
			"pix_fmt": "rgba",
			"s":       fmt.Sprintf("%dx%d", w, h),
		}).
			WithOutput(frames_w).
			Silent(true).
			Run()
		frames_w.Close()
	}()

	// Sound
	cmd := exec.Command(
		"ffmpeg",
		"-i", full_path,
		"-vn",         // no video
		"-f", "s16le", // raw signed 16-bit little-endian
		"-acodec", "pcm_s16le",
		"-ac", "2", // force stereo
		"-ar", "44100", // force 44100 Hz
		"pipe:1",
	)
	cmd_out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Discard stderr (ffmpeg logs go there)
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	finished_audio := make(chan error)
	go func() {
		finished_audio <- cmd.Wait()
	}()

	streamer := &ffmpegStreamer{reader: cmd_out, channels: 2}

	go speaker.Play(beep.Seq(streamer, beep.Callback(func() {})))

	return &video{pixel.R(fx, fy, fx+fw, fy+fh), frames_r, finished_video, false, frame_time, 0, make([]byte, w*h*4),
		finished_audio, false}, nil
}

func (v *video) Tick(seconds float64) {
	if v.IsExpired() {
		return
	}

	if !v.finished_audio_flag {
		select {
		case err := <-v.finished_audio:
			if err != nil {
				fmt.Printf("Audio error: %v", err)
			}
			v.finished_audio_flag = true
		default:
		}
	}

	v.time_since_last += seconds
	if v.time_since_last > 0 {
		if !v.finished_video_flag {
			select {
			case err := <-v.finished_video:
				v.finished_video_flag = true
				if err != nil && err != io.EOF {
					fmt.Printf("Video decoder error: %v", err)
				}
				return
			default:
			}

			_, err := io.ReadFull(v.vid_reader, v.video_buffer)
			if err != nil && err != io.EOF {
				fmt.Println("Video reading error:", err)
			}
		}

		v.time_since_last -= v.frame_time
	}
}

func (v *video) Draw(target pixel.Target) {
	pic := pixel.PictureDataFromImage(&image.RGBA{
		Pix:    v.video_buffer,
		Stride: int(v.rect.W()) * 4,
		Rect:   image.Rect(0, 0, int(v.rect.W()), int(v.rect.H())), // image.Rect is not pixel.Rect.  TODO: preserve int bounds; int-to-float-to-int is smelly
	}) // This handles pixel's upside-down coordinates

	pixel.NewSprite(pic, pic.Bounds()).Draw(target, pixel.IM.Moved(v.rect.Center()))
}

func (v *video) IsExpired() bool {
	return v.finished_video_flag && v.finished_audio_flag
}
