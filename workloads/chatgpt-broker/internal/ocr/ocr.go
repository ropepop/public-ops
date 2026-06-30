package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type Extractor struct {
	TesseractPath string
}

func (e Extractor) ExtractChatGPTAnswer(ctx context.Context, screenshot []byte) (string, error) {
	if len(screenshot) == 0 {
		return "", fmt.Errorf("empty screenshot")
	}
	img, err := png.Decode(bytes.NewReader(screenshot))
	if err != nil {
		return "", err
	}
	prepared := prepareAnswerCrop(img)
	file, err := os.CreateTemp(".", "chatgpt-ocr-*.png")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)
	if err := png.Encode(file, prepared); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	binary := strings.TrimSpace(e.TesseractPath)
	if binary == "" {
		binary = "tesseract"
	}
	out, err := exec.CommandContext(ctx, binary, path, "stdout", "--psm", "6").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tesseract failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	text := cleanText(string(out))
	if text == "" {
		return "", fmt.Errorf("ocr returned no text")
	}
	return text, nil
}

func prepareAnswerCrop(img image.Image) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	left := bounds.Min.X + maxInt(0, width/40)
	right := bounds.Max.X - maxInt(0, width/40)
	if width > height {
		left = bounds.Min.X + (width * 37 / 100)
	}
	top := bounds.Min.Y + height/5
	bottom := bounds.Min.Y + (height * 4 / 5)
	if right <= left || bottom <= top {
		return img
	}
	crop := image.NewGray(image.Rect(0, 0, (right-left)*2, (bottom-top)*2))
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			gray := color.GrayModel.Convert(img.At(x, y)).(color.Gray)
			if gray.Y > 235 {
				gray.Y = 255
			} else if gray.Y < 80 {
				gray.Y = 0
			}
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					crop.SetGray((x-left)*2+dx, (y-top)*2+dy, gray)
				}
			}
		}
	}
	return crop
}

func cleanText(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	noise := regexp.MustCompile(`(?i)^(chatgpt|reply to chatgpt|new chat|edit menu|start a voice conversation|more info|don'?t show again|sending)$`)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "|")
		line = strings.TrimSpace(line)
		if line == "" || noise.MatchString(line) {
			continue
		}
		if len([]rune(line)) <= 2 {
			continue
		}
		if strings.Contains(strings.ToLower(line), "poe") {
			continue
		}
		if len([]rune(line)) < 12 && (strings.Contains(line, "<") || strings.Contains(line, ":")) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "reply exactly:") {
			continue
		}
		if len(cleaned) == 0 || cleaned[len(cleaned)-1] != line {
			cleaned = append(cleaned, line)
		}
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ draw.Image = (*image.Gray)(nil)
