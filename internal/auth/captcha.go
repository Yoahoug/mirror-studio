package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// CaptchaAlphabet is the character set the reference draws codes from.
const CaptchaAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// CaptchaLength matches generate_captcha_code(length=4).
const CaptchaLength = 4

// GenerateCaptchaCode draws a code from CaptchaAlphabet with the system CSPRNG.
// The reference uses random.choice; the alphabet and length are the contract,
// the distribution is not.
func GenerateCaptchaCode(length int) string {
	if length <= 0 {
		length = CaptchaLength
	}
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	out := make([]byte, length)
	for index, value := range buffer {
		out[index] = CaptchaAlphabet[int(value)%len(CaptchaAlphabet)]
	}
	return string(out)
}

// renderCaptchaDataURI reproduces apps/users/utils.py render_captcha_data_uri
// byte for byte, including python's Mersenne-Twister seeding, so the SSR page
// and the API serve identical images. The result is a
// data:image/svg+xml;base64 URI.
func renderCaptchaDataURI(code string) string {
	const width, height = 168, 56
	seed := 0
	for index, char := range code {
		seed += (index + 1) * int(char)
	}
	rng := newPyRandom(uint32(seed))

	var circles strings.Builder
	for i := 0; i < 7; i++ {
		cx := rng.randint(8, width-8)
		cy := rng.randint(8, height-8)
		radius := rng.randint(2, 4)
		red := rng.randint(180, 235)
		green := rng.randint(190, 235)
		fmt.Fprintf(&circles, `<circle cx="%d" cy="%d" r="%d" fill="rgba(%d, %d, 255, 0.32)" />`, cx, cy, radius, red, green)
	}

	var paths strings.Builder
	for i := 0; i < 4; i++ {
		startY := rng.randint(10, height-10)
		controlY := rng.randint(4, height-4)
		endY := rng.randint(10, height-10)
		red := rng.randint(120, 190)
		green := rng.randint(135, 205)
		blue := rng.randint(190, 245)
		strokeWidth := rng.uniform(1.2, 2.1)
		fmt.Fprintf(&paths,
			`<path d="M -8 %d Q %s %d %d %d" stroke="rgba(%d, %d, %d, 0.42)" stroke-width="%s" fill="none" stroke-linecap="round" />`,
			startY, strconv.FormatFloat(float64(width)/2, 'f', 1, 64), controlY, width+8, endY,
			red, green, blue, strconv.FormatFloat(strokeWidth, 'f', 2, 64),
		)
	}

	var glyphs strings.Builder
	const startX, gap = 24, 33
	for index, char := range code {
		x := startX + index*gap + rng.randint(-2, 2)
		y := 36 + rng.randint(-4, 5)
		rotate := rng.randint(-12, 12)
		fontSize := rng.randint(25, 31)
		opacity := rng.uniform(0.84, 0.98)
		fmt.Fprintf(&glyphs,
			`<text x="%d" y="%d" transform="rotate(%d %d %d)" fill="#0f172a" fill-opacity="%s" `+
				`font-family="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace" `+
				`font-size="%d" font-weight="700" letter-spacing="1.5">%s</text>`,
			x, y, rotate, x, y, strconv.FormatFloat(opacity, 'f', 2, 64), fontSize, html.EscapeString(string(char)),
		)
	}

	svg := strings.TrimSpace(fmt.Sprintf(`
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="验证码">
  <defs>
    <linearGradient id="captcha-bg" x1="0%%" y1="0%%" x2="100%%" y2="100%%">
      <stop offset="0%%" stop-color="#f8fbff" />
      <stop offset="100%%" stop-color="#eef4ff" />
    </linearGradient>
    <filter id="soft-noise">
      <feTurbulence type="fractalNoise" baseFrequency="0.9" numOctaves="1" seed="%d" />
      <feColorMatrix type="saturate" values="0" />
      <feComponentTransfer>
        <feFuncA type="table" tableValues="0 0 0.08 0" />
      </feComponentTransfer>
    </filter>
  </defs>
  <rect x="0.75" y="0.75" width="%s" height="%s" rx="16" fill="url(#captcha-bg)" stroke="rgba(148,163,184,0.28)" />
  <rect x="0" y="0" width="%d" height="%d" rx="16" fill="white" filter="url(#soft-noise)" opacity="0.45" />
  %s
  %s
  %s
</svg>`,
		width, height, width, height, seed,
		strconv.FormatFloat(width-1.5, 'f', 1, 64), strconv.FormatFloat(height-1.5, 'f', 1, 64),
		width, height,
		circles.String(), paths.String(), glyphs.String(),
	))
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

// pyRandom is CPython's random.Random with an integer seed, enough to reproduce
// the deterministic captcha artwork. Only seeding plus the used methods are
// implemented.
type pyRandom struct {
	state [624]uint32
	index int
}

func newPyRandom(seed uint32) *pyRandom {
	rng := &pyRandom{}
	rng.state[0] = 19650218
	for i := 1; i < 624; i++ {
		rng.state[i] = 1812433253*(rng.state[i-1]^(rng.state[i-1]>>30)) + uint32(i)
	}
	// init_by_array with the seed as a single 32-bit key word.
	i, j := 1, 0
	for k := 624; k > 0; k-- {
		rng.state[i] = (rng.state[i] ^ ((rng.state[i-1] ^ (rng.state[i-1] >> 30)) * 1664525)) + seed + uint32(j)
		i++
		j++
		if i >= 624 {
			rng.state[0] = rng.state[623]
			i = 1
		}
		if j >= 1 {
			j = 0
		}
	}
	for k := 623; k > 0; k-- {
		rng.state[i] = (rng.state[i] ^ ((rng.state[i-1] ^ (rng.state[i-1] >> 30)) * 1566083941)) - uint32(i)
		i++
		if i >= 624 {
			rng.state[0] = rng.state[623]
			i = 1
		}
	}
	rng.state[0] = 0x80000000
	rng.index = 624
	return rng
}

func (r *pyRandom) genrand() uint32 {
	if r.index >= 624 {
		for i := 0; i < 624; i++ {
			y := (r.state[i] & 0x80000000) | (r.state[(i+1)%624] & 0x7fffffff)
			r.state[i] = r.state[(i+397)%624] ^ (y >> 1)
			if y&1 != 0 {
				r.state[i] ^= 2567483615
			}
		}
		r.index = 0
	}
	y := r.state[r.index]
	r.index++
	y ^= y >> 11
	y ^= (y << 7) & 2636928640
	y ^= (y << 15) & 4022730752
	y ^= y >> 18
	return y
}

// getrandbits mirrors random.Random.getrandbits for k <= 32.
func (r *pyRandom) getrandbits(bits int) uint32 {
	return r.genrand() >> (32 - uint(bits))
}

// randbelow mirrors random.Random._randbelow_with_getrandbits.
func (r *pyRandom) randbelow(n uint32) uint32 {
	if n == 0 {
		return 0
	}
	bits := 1
	for value := n; value > 1; value >>= 1 {
		bits++
	}
	result := r.getrandbits(bits)
	for result >= n {
		result = r.getrandbits(bits)
	}
	return result
}

// randint mirrors random.Random.randint (inclusive bounds).
func (r *pyRandom) randint(low, high int) int {
	width := uint32(high - low + 1)
	return low + int(r.randbelow(width))
}

// random mirrors random.Random.random (53-bit resolution).
func (r *pyRandom) random() float64 {
	first := float64(r.getrandbits(32) >> 5)
	second := float64(r.getrandbits(32) >> 6)
	return (first*67108864.0 + second) / 9007199254740992.0
}

// uniform mirrors random.Random.uniform.
func (r *pyRandom) uniform(low, high float64) float64 {
	return low + (high-low)*r.random()
}
