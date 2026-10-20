package audio

import (
	"errors"
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

// ErrNoTempo means the signal is too short or has no detectable beat.
var ErrNoTempo = errors.New("no tempo detected")

const (
	tempoFrame     = 1024
	tempoHop       = 256
	tempoAvgWindow = 16    // frames in the moving average removed from the flux
	tempoMinBPM    = 60.0  // slowest lag searched
	tempoMaxBPM    = 200.0 // fastest lag searched
	tempoPriorBPM  = 120.0 // centre of the log-Gaussian prior
	tempoPriorSig  = 1.0   // prior width in octaves
	tempoMinScore  = 0.05  // below this, no tempo
	tempoMinSecs   = 10
	// tempoMinVar is the flux variance below which the envelope counts as flat. A steady
	// sine leaves only FFT-leakage jitter (variance ~1e-4); music is orders of magnitude above.
	tempoMinVar = 1e-3
)

// DetectBPM estimates the tempo of mono PCM samples at rate Hz, rounded to 1 decimal.
// Spectral-flux onset envelope, autocorrelation over 60–200 BPM lags weighted by a
// log-Gaussian prior centred on 120 BPM, then parabolic peak refinement.
func DetectBPM(samples []float32, rate int) (float64, error) {
	if rate <= 0 || len(samples) < tempoMinSecs*rate {
		return 0, ErrNoTempo
	}
	flux := onsetEnvelope(samples)
	if !hasVariance(flux) {
		return 0, ErrNoTempo
	}
	fr := float64(rate) / tempoHop
	minL := int(math.Round(60 * fr / tempoMaxBPM))
	maxL := int(math.Round(60 * fr / tempoMinBPM))
	if minL < 1 || maxL+1 >= len(flux) {
		return 0, ErrNoTempo
	}
	acf := make([]float64, maxL+2)
	for l := range acf {
		if l != 0 && l < minL-1 {
			continue
		}
		var sum float64
		for n := 0; n+l < len(flux); n++ {
			sum += flux[n] * flux[n+l]
		}
		acf[l] = sum
	}
	if acf[0] <= 0 {
		return 0, ErrNoTempo
	}
	best, bestScore := 0, 0.0
	for l := minL; l <= maxL; l++ {
		bpm := 60 * fr / float64(l)
		d := math.Log2(bpm/tempoPriorBPM) / tempoPriorSig
		score := acf[l] / acf[0] * math.Exp(-0.5*d*d)
		if score > bestScore {
			best, bestScore = l, score
		}
	}
	if best == 0 || bestScore < tempoMinScore {
		return 0, ErrNoTempo
	}
	lag := float64(best)
	if a, b, c := acf[best-1], acf[best], acf[best+1]; a-2*b+c != 0 {
		if off := 0.5 * (a - c) / (a - 2*b + c); math.Abs(off) <= 1 {
			lag += off
		}
	}
	return math.Round(600*fr/lag) / 10, nil
}

// onsetEnvelope returns the half-wave-rectified spectral flux of log magnitudes,
// with a moving average subtracted and the result clamped at zero.
func onsetEnvelope(samples []float32) []float64 {
	nFrames := (len(samples)-tempoFrame)/tempoHop + 1
	fft := fourier.NewFFT(tempoFrame)
	win := make([]float64, tempoFrame)
	for i := range win {
		win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/tempoFrame)
	}
	buf := make([]float64, tempoFrame)
	var coeff []complex128
	prev := make([]float64, tempoFrame/2+1)
	cur := make([]float64, tempoFrame/2+1)
	flux := make([]float64, nFrames)
	for f := 0; f < nFrames; f++ {
		off := f * tempoHop
		for i := range buf {
			buf[i] = float64(samples[off+i]) * win[i]
		}
		coeff = fft.Coefficients(coeff, buf)
		var sum float64
		for k, c := range coeff {
			cur[k] = math.Log1p(math.Hypot(real(c), imag(c)))
			if f > 0 {
				if d := cur[k] - prev[k]; d > 0 {
					sum += d
				}
			}
		}
		flux[f] = sum
		prev, cur = cur, prev
	}
	out := make([]float64, nFrames)
	var run float64
	for n := range flux {
		run += flux[n]
		if n >= tempoAvgWindow {
			run -= flux[n-tempoAvgWindow]
		}
		w := min(n+1, tempoAvgWindow)
		out[n] = max(0, flux[n]-run/float64(w))
	}
	return out
}

// hasVariance reports whether the envelope is non-zero and not essentially flat.
func hasVariance(x []float64) bool {
	var mx, mean float64
	for _, v := range x {
		mx = max(mx, v)
		mean += v
	}
	if mx == 0 {
		return false
	}
	mean /= float64(len(x))
	var v float64
	for _, e := range x {
		v += (e - mean) * (e - mean)
	}
	v /= float64(len(x))
	return v >= tempoMinVar
}
