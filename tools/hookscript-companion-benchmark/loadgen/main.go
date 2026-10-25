package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	Latency  time.Duration
	Status   int
	Err      string
	Response int
}

type summary struct {
	Path         string  `json:"path"`
	Concurrency  int     `json:"concurrency"`
	Warmup       int     `json:"warmup"`
	Requests     int     `json:"requests"`
	Errors       int     `json:"errors"`
	DurationMS   float64 `json:"duration_ms"`
	Throughput   float64 `json:"throughput_rps"`
	PayloadBytes int     `json:"payload_bytes"`
	MinMS        float64 `json:"min_ms"`
	P50MS        float64 `json:"p50_ms"`
	P90MS        float64 `json:"p90_ms"`
	P95MS        float64 `json:"p95_ms"`
	P99MS        float64 `json:"p99_ms"`
	MaxMS        float64 `json:"max_ms"`
	MeanMS       float64 `json:"mean_ms"`
	Statuses     map[int]int `json:"statuses"`
}

func main() {
	base := flag.String("url", "http://127.0.0.1:15001", "Spinner base URL")
	path := flag.String("path", "/hooks/bench-small", "ingress path")
	workload := flag.String("workload", "small", "small or real")
	concurrency := flag.Int("concurrency", 1, "concurrent clients")
	warmup := flag.Int("warmup", 30, "unmeasured warmup requests")
	requests := flag.Int("requests", 200, "measured requests")
	out := flag.String("out", "", "optional JSON output path")
	flag.Parse()

	if *concurrency < 1 || *requests < 1 {
		fmt.Fprintln(os.Stderr, "concurrency and requests must be positive")
		os.Exit(2)
	}

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency * 2,
		MaxConnsPerHost:     *concurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	defer transport.CloseIdleConnections()

	payloadBytes := payloadSize(*workload)
	if *warmup > 0 {
		run(client, *base, *path, *workload, *concurrency, *warmup, 0)
	}

	start := time.Now()
	results := run(client, *base, *path, *workload, *concurrency, *requests, *warmup)
	elapsed := time.Since(start)

	statuses := map[int]int{}
	errs := 0
	latencies := make([]time.Duration, 0, len(results))
	for _, item := range results {
		statuses[item.Status]++
		if item.Err != "" {
			errs++
			continue
		}
		latencies = append(latencies, item.Latency)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	get := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	s := summary{
		Path: *path, Concurrency: *concurrency, Warmup: *warmup, Requests: *requests,
		Errors: errs, DurationMS: float64(elapsed.Microseconds()) / 1000,
		PayloadBytes: payloadBytes, Statuses: statuses,
	}
	if len(latencies) > 0 {
		s.Throughput = float64(len(latencies)) / elapsed.Seconds()
		s.MinMS = get(latencies[0])
		s.P50MS = get(percentile(latencies, 50))
		s.P90MS = get(percentile(latencies, 90))
		s.P95MS = get(percentile(latencies, 95))
		s.P99MS = get(percentile(latencies, 99))
		s.MaxMS = get(latencies[len(latencies)-1])
		var total time.Duration
		for _, value := range latencies {
			total += value
		}
		s.MeanMS = get(total / time.Duration(len(latencies)))
	}

	encoded, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(encoded))
	if *out != "" {
		if err := os.WriteFile(*out, append(encoded, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if errs > 0 {
		os.Exit(1)
	}
}

func run(client *http.Client, base, path, workload string, concurrency, requests, offset int) []result {
	results := make([]result, requests)
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer wg.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= requests {
					return
				}
				results[index] = invoke(client, base, path, workload, offset+index)
			}
		}()
	}
	wg.Wait()
	return results
}

func invoke(client *http.Client, base, path, workload string, seq int) result {
	body := payload(workload, seq)
	url := fmt.Sprintf("%s%s?seq=%d", strings.TrimRight(base, "/"), path, seq)
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result{Err: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Benchmark-Seq", fmt.Sprintf("%d", seq))

	start := time.Now()
	response, err := client.Do(request)
	latency := time.Since(start)
	if err != nil {
		return result{Latency: latency, Err: err.Error(), Status: -1}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return result{Latency: latency, Err: readErr.Error(), Status: response.StatusCode}
	}
	if response.StatusCode != http.StatusOK {
		return result{Latency: latency, Err: fmt.Sprintf("status %d: %s", response.StatusCode, truncate(raw, 200)), Status: response.StatusCode}
	}
	if !bytes.Contains(raw, []byte(`"ok":true`)) {
		return result{Latency: latency, Err: "response does not contain ok=true: " + truncate(raw, 200), Status: response.StatusCode}
	}
	return result{Latency: latency, Status: response.StatusCode, Response: len(raw)}
}

func payload(workload string, seq int) []byte {
	if workload == "real" {
		items := make([]map[string]any, 24)
		for i := range items {
			items[i] = map[string]any{
				"sku":   fmt.Sprintf("sku-%02d-%d", i, seq%17),
				"qty":   (seq + i) % 7,
				"price": 100 + i*37,
			}
		}
		encoded, _ := json.Marshal(map[string]any{
			"seq":     seq,
			"event":   "benchmark.invoice",
			"tax_bps": 750,
			"customer": map[string]any{
				"id":    fmt.Sprintf("cust-%06d", seq%1000),
				"email": "benchmark@example.test",
			},
			"items": items,
		})
		return encoded
	}

	encoded, _ := json.Marshal(map[string]any{
		"seq":   seq,
		"event": "benchmark.small",
		"value": strings.Repeat("x", 32),
	})
	return encoded
}

func payloadSize(workload string) int { return len(payload(workload, 0)) }

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func truncate(raw []byte, limit int) string {
	if len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "..."
}
