package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"io"
	"io/ioutil"
	"log"
	"os"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
)

// OverlayText represents a single line of text and the coordinates where it should appear.
type OverlayText struct {
	Text string  `json:"text"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

func main() {
	// Command-line flags:
	jsonPath := flag.String("json", "", "Path to JSON file describing text overlay")
	pdfPath := flag.String("pdf", "", "Path to the original PDF to be overlaid")
	outPath := flag.String("out", "out.pdf", "Path to the output PDF file")
	flag.Parse()

	// Basic validation
	if *jsonPath == "" || *pdfPath == "" {
		fmt.Println("Usage: overlay-text -json=overlay.json -pdf=original.pdf -out=result.pdf")
		os.Exit(1)
	}

	// 1. Read overlay JSON data
	data, err := ioutil.ReadFile(*jsonPath)
	if err != nil {
		log.Fatalf("Could not read JSON file: %v\n", err)
	}

	var overlays []OverlayText
	if err := json.Unmarshal(data, &overlays); err != nil {
		log.Fatalf("JSON parse error: %v\n", err)
	}

	// 2. Read the entire original PDF into memory (for multiple passes)
	originalPDF, err := ioutil.ReadFile(*pdfPath)
	if err != nil {
		log.Fatalf("Could not read PDF file: %v\n", err)
	}

	// We'll apply one "watermark" per overlay in memory.
	currentPDF := originalPDF

	for i, overlay := range overlays {
		// 3. Construct the watermark parameter string.
		//    This places text at bottom-left + (dx, dy) = (overlay.X, overlay.Y).
		paramStr := fmt.Sprintf("pos:bl, offset:%f %f, rot:0, scale:1, fillc:#000000, mode:0",
			overlay.X, overlay.Y)

		// 4. Parse the text watermark details (onTop=true, unit=POINTS).
		wm, err := pdfcpu.ParseTextWatermarkDetails(overlay.Text, paramStr, true, types.POINTS)
		if err != nil {
			log.Fatalf("Error creating watermark for overlay %d: %v\n", i, err)
		}

		// 5. Apply the watermark to all pages in-memory.
		inBuf := bytes.NewReader(currentPDF)
		outBuf := new(bytes.Buffer)

		if err := api.AddWatermarks(inBuf, outBuf, nil, wm, nil); err != nil {
			log.Fatalf("pdfcpu AddWatermarks failed at overlay %d: %v\n", i, err)
		}

		// Overwrite `currentPDF` with the newly watermarked data.
		currentPDF, err = io.ReadAll(outBuf)
		if err != nil {
			log.Fatalf("io.ReadAll after watermark failed: %v\n", err)
		}
	}

	// 6. After applying all overlays, write the final PDF to disk.
	if err := os.WriteFile(*outPath, currentPDF, 0644); err != nil {
		log.Fatalf("Could not write output PDF: %v\n", err)
	}

	fmt.Printf("Overlay PDF created successfully: %s\n", *outPath)
}
