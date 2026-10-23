package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type headerFlags []string

func (headers *headerFlags) String() string {
	return strings.Join(*headers, ", ")
}

func (headers *headerFlags) Set(value string) error {
	if !strings.Contains(value, ":") {
		return fmt.Errorf("header must use 'Name: value' format")
	}
	*headers = append(*headers, value)
	return nil
}

type result struct {
	duration time.Duration
	status   int
	err      error
}

func main() {
	var headers headerFlags
	target := flag.String("url", "", "public Gateway ingress URL")
	method := flag.String("method", http.MethodPost, "HTTP request method")
	body := flag.String("body", `{"account":{"id":"account-42"}}`, "request body")
	requests := flag.Int("requests", 5000, "total requests")
	concurrency := flag.Int("concurrency", 50, "concurrent workers")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout")
	flag.Var(&headers, "header", "request header in 'Name: value' format; repeatable")
	flag.Parse()

	if err := benchmark(*target, *method, []byte(*body), headers, *requests, *concurrency, *timeout); err != nil {
		log.Fatal(err)
	}
}

func benchmark(
	rawURL string,
	method string,
	body []byte,
	headers []string,
	requestCount int,
	concurrency int,
	timeout time.Duration,
) error {
	target, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("a valid absolute -url is required")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("benchmark URL must use http or https")
	}
	if requestCount < 1 || concurrency < 1 {
		return fmt.Errorf("requests and concurrency must be positive")
	}
	if concurrency > requestCount {
		concurrency = requestCount
	}

	parsedHeaders := make(http.Header)
	for _, raw := range headers {
		name, value, _ := strings.Cut(raw, ":")
		parsedHeaders.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	if parsedHeaders.Get("Content-Type") == "" {
		parsedHeaders.Set("Content-Type", "application/json")
	}

	transport := &http.Transport{
		MaxIdleConns:        concurrency * 2,
		MaxIdleConnsPerHost: concurrency,
		MaxConnsPerHost:     concurrency,
		IdleConnTimeout:     90 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	jobs := make(chan struct{})
	results := make(chan result, requestCount)

	var workers sync.WaitGroup
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for range jobs {
				started := time.Now()
				request, requestErr := http.NewRequest(strings.ToUpper(method), target.String(), bytes.NewReader(body))
				if requestErr != nil {
					results <- result{duration: time.Since(started), err: requestErr}
					continue
				}
				request.Header = parsedHeaders.Clone()

				response, requestErr := client.Do(request)
				if requestErr != nil {
					results <- result{duration: time.Since(started), err: requestErr}
					continue
				}
				_, copyErr := io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if copyErr != nil {
					requestErr = copyErr
				} else if closeErr != nil {
					requestErr = closeErr
				}
				results <- result{duration: time.Since(started), status: response.StatusCode, err: requestErr}
			}
		}()
	}

	started := time.Now()
	go func() {
		for index := 0; index < requestCount; index++ {
			jobs <- struct{}{}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	durations := make([]time.Duration, 0, requestCount)
	statuses := make(map[int]int)
	errors := 0
	for result := range results {
		durations = append(durations, result.duration)
		if result.err != nil {
			errors++
			continue
		}
		statuses[result.status]++
	}
	elapsed := time.Since(started)
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })

	var total time.Duration
	for _, duration := range durations {
		total += duration
	}
	mean := total / time.Duration(len(durations))
	statusCodes := make([]int, 0, len(statuses))
	for status := range statuses {
		statusCodes = append(statusCodes, status)
	}
	sort.Ints(statusCodes)
	statusParts := make([]string, 0, len(statusCodes))
	for _, status := range statusCodes {
		statusParts = append(statusParts, fmt.Sprintf("%d=%d", status, statuses[status]))
	}

	fmt.Printf("requests=%d concurrency=%d elapsed=%s throughput=%.1f req/s errors=%d statuses=[%s]\n",
		requestCount,
		concurrency,
		elapsed.Round(time.Millisecond),
		float64(requestCount)/elapsed.Seconds(),
		errors,
		strings.Join(statusParts, " "),
	)
	fmt.Printf("latency mean=%s p50=%s p95=%s p99=%s max=%s\n",
		mean.Round(time.Microsecond),
		percentile(durations, 0.50).Round(time.Microsecond),
		percentile(durations, 0.95).Round(time.Microsecond),
		percentile(durations, 0.99).Round(time.Microsecond),
		durations[len(durations)-1].Round(time.Microsecond),
	)

	return nil
}

func percentile(sorted []time.Duration, value float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted))*value+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
