package utils

import "errors"
import "unicode"
import "strconv"

import	"image/color"
	
// Color_from_RGBstring converts an ini file colour string (e.g. "R255g128b0") into a color.RGBA
// the alpha part of RGBA just gets set to 0xff (full opacity) if omitted
// (r, g, and b get set to 0 if omitted, which means "g42" or even an empty string is technically a valid color string, but please don't do that)
func Colour_from_RGBstring(str string) (color.RGBA, error) {
	out := color.RGBA{0, 0, 0, 0xFF}

	name := rune(0)
	numstr := ""
	for _, r := range str + "!" { // +"!" is an evil way to make sure the final colour index gets processed.
		if unicode.IsDigit(r) {
			numstr += string(r)
		} else {
			if name != 0 {
				number, _ := strconv.Atoi(numstr)
				if number > 255 {
					number = 255
				}
				switch name {
				case 'r', 'R':
					out.R = uint8(number)
				case 'g', 'G':
					out.G = uint8(number)
				case 'b', 'B':
					out.B = uint8(number)
				case 'a', 'A':
					out.A = uint8(number)
				default:
					return out, errors.New("Unexpected colour index (not 'r', 'b' or 'g'): " + string(name))
				}
				numstr = ""
			}
			name = r
		}
	}
	return out, nil
}