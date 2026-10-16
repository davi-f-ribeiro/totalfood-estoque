package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Roteirização (fase 3). O OSRM (serviço externo, licença BSD) dá a matriz de tempo e
// distância pelas ruas; a ordem das paradas sai daqui, com vizinho mais próximo e
// melhoria 2-opt. Sem OSRM_URL, ou com o OSRM fora do ar, a matriz é estimada pela
// distância em linha reta, e a resposta diz que é estimativa.

type latLng struct{ Lat, Lng float64 }

type travelMatrix struct {
	Durations [][]float64 // segundos, [de][para]
	Distances [][]float64 // metros, [de][para]
}

const (
	osrmTimeout = 5 * time.Second
	// Estimativa sem OSRM: rua é ~30% mais longa que a linha reta, a 30 km/h na cidade.
	estimateDetourFactor = 1.3
	estimateSpeedMS      = 30.0 / 3.6
)

func osrmBaseURL() string { return strings.TrimRight(strings.TrimSpace(os.Getenv("OSRM_URL")), "/") }

var osrmHTTPClient = &http.Client{Timeout: osrmTimeout}

// osrmTable pede ao OSRM a matriz entre todos os pontos (serviço table/v1).
func osrmTable(ctx context.Context, base string, pts []latLng) (travelMatrix, error) {
	coords := make([]string, len(pts))
	for i, p := range pts {
		// O OSRM recebe longitude antes de latitude.
		coords[i] = strconv.FormatFloat(p.Lng, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
	}
	url := base + "/table/v1/driving/" + strings.Join(coords, ";") + "?annotations=duration,distance"
	ctx, cancel := context.WithTimeout(ctx, osrmTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return travelMatrix{}, err
	}
	resp, err := osrmHTTPClient.Do(req)
	if err != nil {
		return travelMatrix{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return travelMatrix{}, err
	}
	var out struct {
		Code      string       `json:"code"`
		Message   string       `json:"message"`
		Durations [][]*float64 `json:"durations"`
		Distances [][]*float64 `json:"distances"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return travelMatrix{}, fmt.Errorf("osrm status %d: invalid json: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || out.Code != "Ok" {
		return travelMatrix{}, fmt.Errorf("osrm status %d code %q: %s", resp.StatusCode, out.Code, out.Message)
	}
	m := travelMatrix{Durations: make([][]float64, len(pts)), Distances: make([][]float64, len(pts))}
	if len(out.Durations) != len(pts) || len(out.Distances) != len(pts) {
		return travelMatrix{}, errors.New("osrm matrix size mismatch")
	}
	for i := range pts {
		if len(out.Durations[i]) != len(pts) || len(out.Distances[i]) != len(pts) {
			return travelMatrix{}, errors.New("osrm matrix size mismatch")
		}
		m.Durations[i] = make([]float64, len(pts))
		m.Distances[i] = make([]float64, len(pts))
		for j := range pts {
			// null quer dizer que não há caminho pelas ruas entre os dois pontos.
			if out.Durations[i][j] == nil || out.Distances[i][j] == nil {
				return travelMatrix{}, fmt.Errorf("osrm has no road between points %d and %d", i, j)
			}
			m.Durations[i][j] = *out.Durations[i][j]
			m.Distances[i][j] = *out.Distances[i][j]
		}
	}
	return m, nil
}

func haversineMeters(a, b latLng) float64 {
	const earthRadius = 6371000.0
	rad := math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLng := (b.Lng - a.Lng) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
}

func estimatedMatrix(pts []latLng) travelMatrix {
	m := travelMatrix{Durations: make([][]float64, len(pts)), Distances: make([][]float64, len(pts))}
	for i := range pts {
		m.Durations[i] = make([]float64, len(pts))
		m.Distances[i] = make([]float64, len(pts))
		for j := range pts {
			d := haversineMeters(pts[i], pts[j]) * estimateDetourFactor
			m.Distances[i][j] = d
			m.Durations[i][j] = d / estimateSpeedMS
		}
	}
	return m
}

// travelMatrixFor usa o OSRM quando configurado e cai para a estimativa se ele falhar.
func travelMatrixFor(ctx context.Context, pts []latLng) (m travelMatrix, source string, err error) {
	if base := osrmBaseURL(); base != "" {
		m, err = osrmTable(ctx, base, pts)
		if err == nil {
			return m, "osrm", nil
		}
	}
	return estimatedMatrix(pts), "estimativa", err
}

// solveOpenPath ordena os pontos 1..n saindo do ponto 0, sem voltar ao início
// (o caminhão termina na última entrega). Minimiza o tempo total.
func solveOpenPath(durations [][]float64) []int {
	n := len(durations)
	if n <= 1 {
		return nil
	}
	visited := make([]bool, n)
	visited[0] = true
	path := make([]int, 0, n-1)
	cur := 0
	for len(path) < n-1 {
		best, bestCost := -1, math.Inf(1)
		for j := 1; j < n; j++ {
			if !visited[j] && durations[cur][j] < bestCost {
				best, bestCost = j, durations[cur][j]
			}
		}
		visited[best] = true
		path = append(path, best)
		cur = best
	}
	cost := func(p []int) float64 {
		total, prev := 0.0, 0
		for _, k := range p {
			total += durations[prev][k]
			prev = k
		}
		return total
	}
	// Melhora enquanto der: 2-opt (inverte um trecho) e realocação (tira uma parada e
	// põe em outra posição). Só o 2-opt fica preso em casos simples, como paradas dos
	// dois lados do depósito. A matriz pode ser assimétrica (mão única), por isso o
	// custo é recalculado inteiro em vez de só nas pontas; com até 99 paradas é rápido.
	best := cost(path)
	try := func(candidate []int) bool {
		if c := cost(candidate); c < best-1e-9 {
			best = c
			copy(path, candidate)
			return true
		}
		return false
	}
	candidate := make([]int, len(path))
	for improved := true; improved; {
		improved = false
		for i := 0; i < len(path)-1; i++ {
			for k := i + 1; k < len(path); k++ {
				copy(candidate, path)
				reverse(candidate, i, k)
				improved = try(candidate) || improved
			}
		}
		for i := range path {
			for k := range path {
				if i == k {
					continue
				}
				relocate(candidate, path, i, k)
				improved = try(candidate) || improved
			}
		}
	}
	return path
}

// relocate copia src em dst com o item da posição from movido para a posição to.
func relocate(dst, src []int, from, to int) {
	moved := src[from]
	rest := dst[:0]
	for i, v := range src {
		if i != from {
			rest = append(rest, v)
		}
	}
	rest = rest[:len(src)-1]
	copy(dst[to+1:], rest[to:])
	dst[to] = moved
}

func reverse(p []int, i, k int) {
	for ; i < k; i, k = i+1, k-1 {
		p[i], p[k] = p[k], p[i]
	}
}
