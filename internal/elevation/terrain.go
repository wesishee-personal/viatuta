// Package elevation attaches ground height to graph nodes and turns it into
// per-edge gradient.
//
// The source is the Terrain Tiles dataset on AWS Open Data, which publishes
// elevation as ordinary RGB PNG tiles in the "terrarium" encoding. That
// choice matters practically: PNG decoding is in Go's standard library, so
// elevation needs no GeoTIFF dependency, no API key, and no rate limit.
package elevation

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// TileURL is the public Terrain Tiles endpoint.
	TileURL = "https://s3.amazonaws.com/elevation-tiles-prod/terrarium/%d/%d/%d.png"

	// Zoom 13 gives about 17 m per pixel at Austin's latitude — finer than
	// the 97 m mean edge length, so a segment's endpoints land in different
	// pixels and the gradient is real rather than quantisation noise.
	Zoom = 13

	tileSize = 256
)

// Tiles holds downloaded elevation tiles and samples heights from them.
type Tiles struct {
	zoom  int
	mu    sync.RWMutex
	tiles map[[2]int][]float32 // tile key -> tileSize*tileSize heights in metres
	cache string
}

func New(cacheDir string) *Tiles {
	return &Tiles{zoom: Zoom, tiles: map[[2]int][]float32{}, cache: cacheDir}
}

// tileXY converts a coordinate to fractional tile coordinates at this zoom.
//
// This is the standard Web Mercator "slippy map" projection. The latitude
// term is the Mercator one: asinh(tan(lat)) is the inverse Gudermannian, the
// same function that makes Greenland look enormous on a world map.
func (t *Tiles) tileXY(lat, lon float64) (fx, fy float64) {
	n := math.Exp2(float64(t.zoom))
	fx = (lon + 180) / 360 * n
	latRad := lat * math.Pi / 180
	fy = (1 - math.Asinh(math.Tan(latRad))/math.Pi) / 2 * n
	return fx, fy
}

// Cover downloads every tile needed for a bounding box.
func (t *Tiles) Cover(ctx context.Context, minLat, minLon, maxLat, maxLon float64,
	logger *slog.Logger) error {

	// Note the flip: tile Y increases southward, so the northern edge of the
	// box gives the smaller Y.
	x0f, y0f := t.tileXY(maxLat, minLon)
	x1f, y1f := t.tileXY(minLat, maxLon)

	x0, y0 := int(math.Floor(x0f)), int(math.Floor(y0f))
	x1, y1 := int(math.Floor(x1f)), int(math.Floor(y1f))

	var keys [][2]int
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			keys = append(keys, [2]int{x, y})
		}
	}
	logger.Info("downloading elevation tiles", "count", len(keys), "zoom", t.zoom)

	if err := os.MkdirAll(t.cache, 0o755); err != nil {
		return fmt.Errorf("elevation: creating cache dir: %w", err)
	}

	// Eight at a time: enough to saturate the link, few enough to stay a
	// polite client of a free public dataset.
	const workers = 8
	jobs := make(chan [2]int)
	errs := make(chan error, len(keys))
	var wg sync.WaitGroup

	client := &http.Client{Timeout: 60 * time.Second}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range jobs {
				if err := t.fetchTile(ctx, client, k); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for _, k := range keys {
		select {
		case jobs <- k:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)

	if err := <-errs; err != nil {
		return err
	}
	logger.Info("elevation tiles ready", "tiles", len(t.tiles))
	return nil
}

// fetchTile loads one tile, from the local cache when possible.
//
// Caching is not merely an optimisation: re-running the ingest is routine
// while tuning, and re-downloading hundreds of tiles each time would be rude
// to a free service.
func (t *Tiles) fetchTile(ctx context.Context, client *http.Client, key [2]int) error {
	path := filepath.Join(t.cache, fmt.Sprintf("%d_%d_%d.png", t.zoom, key[0], key[1]))

	data, err := os.ReadFile(path)
	if err != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf(TileURL, t.zoom, key[0], key[1]), nil)
		if err != nil {
			return fmt.Errorf("elevation: building request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("elevation: fetching tile %v: %w", key, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return nil // ocean or out of coverage; absent is not an error
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("elevation: tile %v returned %s", key, resp.Status)
		}
		if data, err = io.ReadAll(resp.Body); err != nil {
			return fmt.Errorf("elevation: reading tile %v: %w", key, err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("elevation: caching tile %v: %w", key, err)
		}
	}

	heights, err := decodeTerrarium(data)
	if err != nil {
		return fmt.Errorf("elevation: decoding tile %v: %w", key, err)
	}

	t.mu.Lock()
	t.tiles[key] = heights
	t.mu.Unlock()
	return nil
}

// decodeTerrarium converts a terrarium PNG to metres.
//
// The encoding packs height into the three colour channels:
//
//	elevation = (R * 256 + G + B / 256) - 32768
//
// The 32768 offset lets the format carry elevations below sea level, and the
// blue channel supplies 1/256 m of sub-metre precision.
func decodeTerrarium(data []byte) ([]float32, error) {
	img, err := png.Decode(newByteReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() != tileSize || b.Dy() != tileSize {
		return nil, fmt.Errorf("unexpected tile size %dx%d", b.Dx(), b.Dy())
	}

	out := make([]float32, tileSize*tileSize)
	// The fast path: terrarium tiles are 8-bit RGB, so the pixel bytes can
	// be read directly rather than through the generic colour interface.
	if rgba, ok := img.(*image.NRGBA); ok {
		for y := 0; y < tileSize; y++ {
			row := rgba.Pix[y*rgba.Stride:]
			for x := 0; x < tileSize; x++ {
				r, g, bl := row[x*4], row[x*4+1], row[x*4+2]
				out[y*tileSize+x] = float32(int(r)*256+int(g)) + float32(bl)/256 - 32768
			}
		}
		return out, nil
	}
	for y := 0; y < tileSize; y++ {
		for x := 0; x < tileSize; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out[y*tileSize+x] = float32(int(r>>8)*256+int(g>>8)) + float32(bl>>8)/256 - 32768
		}
	}
	return out, nil
}

// At returns the ground height at a coordinate, in metres, bilinearly
// interpolated between the four surrounding samples.
//
// Interpolation matters here. Nearest-neighbour sampling would quantise every
// node to a 17 m grid, and two nodes landing in the same pixel would report a
// gradient of exactly zero while two straddling a boundary would report a
// step — turning a smooth hill into a staircase of false flats and cliffs.
func (t *Tiles) At(lat, lon float64) (float32, bool) {
	fx, fy := t.tileXY(lat, lon)

	// Position in global pixel space, offset by half a pixel because a
	// sample sits at the centre of its pixel, not its corner.
	px := fx*tileSize - 0.5
	py := fy*tileSize - 0.5

	x0, y0 := math.Floor(px), math.Floor(py)
	dx, dy := px-x0, py-y0

	v00, ok00 := t.pixel(int(x0), int(y0))
	v10, ok10 := t.pixel(int(x0)+1, int(y0))
	v01, ok01 := t.pixel(int(x0), int(y0)+1)
	v11, ok11 := t.pixel(int(x0)+1, int(y0)+1)
	if !ok00 || !ok10 || !ok01 || !ok11 {
		if ok00 {
			return v00, true // at the edge of coverage, one sample will do
		}
		return 0, false
	}

	top := v00*(1-float32(dx)) + v10*float32(dx)
	bot := v01*(1-float32(dx)) + v11*float32(dx)
	return top*(1-float32(dy)) + bot*float32(dy), true
}

// pixel reads one global pixel, resolving which tile holds it.
func (t *Tiles) pixel(gx, gy int) (float32, bool) {
	key := [2]int{gx / tileSize, gy / tileSize}
	if gx < 0 || gy < 0 {
		return 0, false
	}
	t.mu.RLock()
	tile, ok := t.tiles[key]
	t.mu.RUnlock()
	if !ok {
		return 0, false
	}
	return tile[(gy%tileSize)*tileSize+(gx%tileSize)], true
}

// byteReader adapts a byte slice to io.Reader without pulling in bytes.
type byteReader struct {
	b []byte
	i int
}

func newByteReader(b []byte) *byteReader { return &byteReader{b: b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
