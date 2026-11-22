package localmusic

import (
	"encoding/binary"
	"io"
	"time"
)

type readAtSeeker interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

func duration(file readAtSeeker, size int64, ext string) time.Duration {
	header := make([]byte, 64)
	if _, err := file.ReadAt(header, 0); err != nil {
		return 0
	}
	if string(header[:4]) == "fLaC" {
		packed := binary.BigEndian.Uint64(header[18:26])
		rate, samples := packed>>44, packed&((1<<36)-1)
		if rate > 0 {
			return time.Duration(samples) * time.Second / time.Duration(rate)
		}
	}
	if string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE" {
		var rate uint32
		for offset := int64(12); offset+8 <= size; {
			var chunk [8]byte
			if _, err := file.ReadAt(chunk[:], offset); err != nil {
				break
			}
			length := int64(binary.LittleEndian.Uint32(chunk[4:8]))
			if string(chunk[:4]) == "fmt " && length >= 16 && offset+8+16 <= size {
				var data [16]byte
				if _, err := file.ReadAt(data[:], offset+8); err != nil {
					break
				}
				rate = binary.LittleEndian.Uint32(data[8:12])
			}
			if string(chunk[:4]) == "data" && rate > 0 {
				return time.Duration(length) * time.Second / time.Duration(rate)
			}
			offset += 8 + length + length%2
		}
	}
	if string(header[4:8]) == "ftyp" {
		return mp4Duration(file, 0, size, 0)
	}
	if ext != "" && ext != ".mp3" {
		return 0
	}
	return mp3Duration(file, size, header)
}

func mp4Duration(file readAtSeeker, start, end int64, depth int) time.Duration {
	if depth > 4 {
		return 0
	}
	for offset := start; offset+8 <= end; {
		var header [8]byte
		if _, err := file.ReadAt(header[:], offset); err != nil {
			return 0
		}
		size, prefix := int64(binary.BigEndian.Uint32(header[:4])), int64(8)
		if size == 1 {
			if offset+16 > end {
				return 0
			}
			var extSize [8]byte
			if _, err := file.ReadAt(extSize[:], offset+8); err != nil {
				return 0
			}
			size, prefix = int64(binary.BigEndian.Uint64(extSize[:])), 16
		}
		if size == 0 {
			size = end - offset
		}
		if size < prefix || size > end-offset {
			return 0
		}
		switch string(header[4:8]) {
		case "moov":
			if value := mp4Duration(file, offset+prefix, offset+size, depth+1); value > 0 {
				return value
			}
		case "mvhd":
			var data [32]byte
			if _, err := file.ReadAt(data[:], offset+prefix); err != nil {
				return 0
			}
			rate, ticks := binary.BigEndian.Uint32(data[12:16]), uint64(binary.BigEndian.Uint32(data[16:20]))
			if data[0] == 1 {
				rate, ticks = binary.BigEndian.Uint32(data[20:24]), binary.BigEndian.Uint64(data[24:32])
			}
			if rate > 0 {
				return time.Duration(float64(ticks) / float64(rate) * float64(time.Second))
			}
		}
		offset += size
	}
	return 0
}

func mp3Duration(file readAtSeeker, size int64, header []byte) time.Duration {
	start := int64(0)
	if string(header[:3]) == "ID3" {
		for _, value := range header[6:10] {
			start = start<<7 | int64(value&127)
		}
		start += 10
	}
	if start >= size {
		return 0
	}
	file.Seek(start, io.SeekStart)
	data := make([]byte, 64<<10)
	count, _ := file.Read(data)
	data = data[:count]
	for index := 0; index+4 <= len(data); index++ {
		bits := binary.BigEndian.Uint32(data[index : index+4])
		version, layer, bitrateIndex, sampleIndex := bits>>19&3, bits>>17&3, bits>>12&15, bits>>10&3
		if bits>>21 != 0x7ff || version == 1 || layer != 1 || bitrateIndex == 0 || bitrateIndex == 15 || sampleIndex == 3 {
			continue
		}
		rate := []int64{44100, 48000, 32000}[sampleIndex]
		bitrate := []int64{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}[bitrateIndex]
		samples, side := int64(1152), 32
		mono := bits>>6&3 == 3
		if mono {
			side = 17
		}
		scale := int64(144000)
		if version != 3 {
			rate /= 2
			if version == 0 {
				rate /= 2
			}
			bitrate = []int64{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}[bitrateIndex]
			samples, side = 576, 17
			if mono {
				side = 9
			}
			scale = 72000
		}
		crc := 0
		if bits&(1<<16) == 0 {
			crc = 2
		}
		for _, pos := range []int{index + 4 + crc + side, index + 4 + side} {
			if pos+12 <= len(data) && (string(data[pos:pos+4]) == "Xing" || string(data[pos:pos+4]) == "Info") && binary.BigEndian.Uint32(data[pos+4:pos+8])&1 != 0 {
				frames := int64(binary.BigEndian.Uint32(data[pos+8 : pos+12]))
				return time.Duration(frames*samples) * time.Second / time.Duration(rate)
			}
		}
		padding := int((bits >> 9) & 1)
		frameLen := int(scale*bitrate/rate) + padding
		if frameLen >= 4 && index+frameLen+4 <= len(data) {
			next := binary.BigEndian.Uint32(data[index+frameLen : index+frameLen+4])
			if next>>21 != 0x7ff || next>>19&3 != version || next>>17&3 != layer || next>>10&3 != sampleIndex {
				continue
			}
		}
		remaining := size - start - int64(index)
		if remaining <= 0 {
			return 0
		}
		return time.Duration(remaining*8) * time.Second / time.Duration(bitrate*1000)
	}
	return 0
}
