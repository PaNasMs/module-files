package localfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
)

func Thumbnail(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := f.Stat()
	if err != nil || s.Size() > 64<<20 {
		return nil, errors.New("unsupported thumbnail source")
	}
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, errors.New("image dimensions exceed thumbnail limit")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	prefix := make([]byte, 1<<17)
	n, _ := f.Read(prefix)
	orientation := jpegOrientation(prefix[:n])
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	original, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	w, h := config.Width, config.Height
	if w > 256 || h > 256 {
		if w >= h {
			h = max(1, h*256/w)
			w = 256
		} else {
			w = max(1, w*256/h)
			h = 256
		}
	}
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(scaled, scaled.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), original, original.Bounds(), draw.Over, nil)
	var result image.Image = scaled
	if orientation > 1 && orientation <= 8 {
		ow, oh := w, h
		if orientation >= 5 {
			ow, oh = h, w
		}
		turned := image.NewRGBA(image.Rect(0, 0, ow, oh))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dx, dy := x, y
				switch orientation {
				case 2:
					dx = w - 1 - x
				case 3:
					dx, dy = w-1-x, h-1-y
				case 4:
					dy = h - 1 - y
				case 5:
					dx, dy = y, x
				case 6:
					dx, dy = h-1-y, x
				case 7:
					dx, dy = h-1-y, w-1-x
				case 8:
					dx, dy = y, w-1-x
				}
				turned.Set(dx, dy, scaled.At(x, y))
			}
		}
		result = turned
	}
	var out bytes.Buffer
	err = jpeg.Encode(&out, result, &jpeg.Options{Quality: 82})
	return out.Bytes(), err
}
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 1
		}
		marker := data[i+1]
		if marker == 0xda || marker == 0xd9 {
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if length < 2 || i+2+length > len(data) {
			return 1
		}
		segment := data[i+4 : i+2+length]
		i += 2 + length
		if marker != 0xe1 || len(segment) < 14 || string(segment[:6]) != "Exif\x00\x00" {
			continue
		}
		d := segment[6:]
		var order binary.ByteOrder
		if string(d[:2]) == "II" {
			order = binary.LittleEndian
		} else if string(d[:2]) == "MM" {
			order = binary.BigEndian
		} else {
			return 1
		}
		offset := uint64(order.Uint32(d[4:8]))
		if offset+2 > uint64(len(d)) {
			return 1
		}
		count := int(order.Uint16(d[offset : offset+2]))
		for j := 0; j < count; j++ {
			p := int(offset) + 2 + j*12
			if p+12 > len(d) {
				return 1
			}
			if order.Uint16(d[p:p+2]) == 0x0112 && order.Uint16(d[p+2:p+4]) == 3 && order.Uint32(d[p+4:p+8]) == 1 {
				return int(order.Uint16(d[p+8 : p+10]))
			}
		}
		return 1
	}
	return 1
}
