package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// OverlayRectText describes one overlay: a white rectangle and text on top.
type OverlayRectText struct {
	Text   string  `json:"text"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`  // rectangle width in PDF points
	Height float64 `json:"height"` // rectangle height in PDF points
	Scale  float64 `json:"scale"`
}

// createWhitePNG returns a data URI for a w x h PNG of solid white.
func createWhitePNG(w, h int) (string, error) {
	// Create a w x h white image.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	// Encode to PNG in memory.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	// Return a data URI: "data:image/png;base64,ABC..."
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return "data:image/png;base64," + encoded, nil
}

// saveDataURIToTempFile takes a data URI from createWhitePNG,
// decodes it, and saves the raw PNG bytes into a temporary file under /tmp.
func saveDataURIToTempFile(dataURI string) (string, error) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURI, prefix) {
		return "", errors.New("not a valid PNG data URI")
	}
	base64Data := dataURI[len(prefix):]

	// Decode the base64 string back into raw PNG bytes
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("base64 decode error: %v", err)
	}

	// Create a temporary file in /tmp
	tmpFile, err := os.CreateTemp("", "white_*.png")
	if err != nil {
		return "", fmt.Errorf("failed creating temp file: %v", err)
	}
	defer tmpFile.Close()

	// Write the PNG bytes to it
	if _, err := tmpFile.Write(decoded); err != nil {
		return "", fmt.Errorf("failed writing to temp file: %v", err)
	}

	return tmpFile.Name(), nil
}

func main() {
	// CLI flags
	jsonPath := flag.String("json", "", "Path to JSON file describing rectangle+text overlays")
	pdfPath := flag.String("pdf", "", "Path to the original PDF")
	outPath := flag.String("out", "out.pdf", "Path to the output PDF file")
	flag.Parse()

	// Basic validation
	if *jsonPath == "" || *pdfPath == "" {
		fmt.Println("Usage: overlay-rect-text -json=overlays.json -pdf=original.pdf -out=modified.pdf")
		os.Exit(1)
	}

	// 1) Read JSON describing overlays
	data, err := ioutil.ReadFile(*jsonPath)
	if err != nil {
		log.Fatalf("Could not read JSON file: %v\n", err)
	}
	var overlays []OverlayRectText
	if err := json.Unmarshal(data, &overlays); err != nil {
		log.Fatalf("JSON parse error: %v\n", err)
	}

	// 2) Load the original PDF into memory (as bytes).
	originalPDF, err := ioutil.ReadFile(*pdfPath)
	if err != nil {
		log.Fatalf("Could not read PDF file: %v\n", err)
	}

	// We'll apply 2 watermarks per overlay (rectangle, then text) in memory.
	currentPDF := originalPDF

	for i, ov := range overlays {
		log.Printf("Processing overlay %d: text=%q at (%.2f, %.2f), rect=%.2fx%.2f, scale=%.2f\n",
			i, ov.Text, ov.X, ov.Y, ov.Width, ov.Height, ov.Scale)

		// -----------------------------------------------------
		// Pass 1: White rectangle (if width/height > 0)
		// -----------------------------------------------------
		if ov.Width > 0 && ov.Height > 0 {
			// Create a data URI for a white PNG of size (ov.Width x ov.Height) in pixels
			// because we'll apply scale:1 abs in pdfcpu => it becomes exactly that many PDF points.
			wInt := int(ov.Width)
			hInt := int(ov.Height)
			whitePNGData, err := createWhitePNG(wInt, hInt)
			if err != nil {
				log.Fatalf("Failed to create white PNG: %v\n", err)
			}
			whitePNGPath, err := saveDataURIToTempFile(whitePNGData)
			if err != nil {
				log.Fatalf("Failed to save white PNG: %v\n", err)
			}
			// Build the parameter string for the image watermark
			// pos:bl => anchor at bottom-left
			// offset:X Y => shift by (ov.X, ov.Y)
			// scale:1 abs => keep actual pixel size => ov.Width x ov.Height in PDF points
			// mode:0 => overlay in the foreground (opaque)
			rectParams := fmt.Sprintf("pos:bl, offset:%f %f, scale:%f abs, rot:0, mode:0", ov.X, ov.Y, ov.Scale)
			wmRect, err := pdfcpu.ParseImageWatermarkDetails(whitePNGPath, rectParams, true, types.POINTS)
			if err != nil {
				log.Fatalf("Failed to parse image watermark details for overlay %d: %v\n", i, err)
			}

			// Apply this rectangle watermark in-memory
			inBuf := bytes.NewReader(currentPDF)
			outBuf := new(bytes.Buffer)

			if err := api.AddWatermarks(inBuf, outBuf, nil, wmRect, nil); err != nil {
				log.Fatalf("Failed adding white rectangle for overlay %d: %v\n", i, err)
			}

			updated, err := io.ReadAll(outBuf)
			if err != nil {
				log.Fatalf("io.ReadAll (rectangle pass) failed: %v\n", err)
			}
			currentPDF = updated
		}

		// -----------------------------------------------------
		// Pass 2: Text
		// -----------------------------------------------------
		textParams := fmt.Sprintf("pos:bl, offset:%f %f, rot:0, scale:%f, fillc:#000000, mode:0",
			ov.X, ov.Y, ov.Scale/4)
		wmText, err := pdfcpu.ParseTextWatermarkDetails(ov.Text, textParams, true, types.POINTS)
		if err != nil {
			log.Fatalf("Error creating text watermark for overlay %d: %v\n", i, err)
		}

		// Apply the text watermark in-memory
		inBuf2 := bytes.NewReader(currentPDF)
		outBuf2 := new(bytes.Buffer)

		if err := api.AddWatermarks(inBuf2, outBuf2, nil, wmText, nil); err != nil {
			log.Fatalf("Failed adding text for overlay %d: %v\n", i, err)
		}

		updated, err := io.ReadAll(outBuf2)
		if err != nil {
			log.Fatalf("io.ReadAll (text pass) failed: %v\n", err)
		}
		currentPDF = updated
	}

	// 3) Write the final PDF
	if err := os.WriteFile(*outPath, currentPDF, 0644); err != nil {
		log.Fatalf("Could not write output PDF: %v\n", err)
	}

	fmt.Printf("Done! Overlays applied. Result saved to %q\n", *outPath)
}
