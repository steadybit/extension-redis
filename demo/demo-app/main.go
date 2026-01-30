/*
 * Demo application for Redis chaos engineering experiments.
 *
 * This app simulates a typical web application that uses Redis for:
 * - Session management
 * - API response caching
 * - Rate limiting
 * - Leaderboards/counters
 *
 * Run modes:
 * - APP mode (default): HTTP server with cached endpoints
 * - LOADGEN mode: Generate continuous traffic to the app
 */

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	redisClient *redis.Client
	stats       = &Statistics{}
)

type Statistics struct {
	TotalRequests  int64 `json:"total_requests"`
	CacheHits      int64 `json:"cache_hits"`
	CacheMisses    int64 `json:"cache_misses"`
	Errors         int64 `json:"errors"`
	AvgLatencyMs   int64 `json:"avg_latency_ms"`
	latencySum     int64
	latencyCount   int64
	mu             sync.Mutex
}

func (s *Statistics) RecordRequest(hit bool, latencyMs int64, err bool) {
	atomic.AddInt64(&s.TotalRequests, 1)
	if hit {
		atomic.AddInt64(&s.CacheHits, 1)
	} else {
		atomic.AddInt64(&s.CacheMisses, 1)
	}
	if err {
		atomic.AddInt64(&s.Errors, 1)
	}
	s.mu.Lock()
	s.latencySum += latencyMs
	s.latencyCount++
	if s.latencyCount > 0 {
		s.AvgLatencyMs = s.latencySum / s.latencyCount
	}
	s.mu.Unlock()
}

func main() {
	mode := os.Getenv("MODE")

	// Initialize Redis client
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	password := os.Getenv("REDIS_PASSWORD")

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	opts.Password = password
	opts.PoolSize = 20
	opts.MinIdleConns = 5

	redisClient = redis.NewClient(opts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	if mode == "loadgen" {
		runLoadGenerator()
	} else {
		runAppServer()
	}
}

// ============================================
// APP SERVER MODE
// ============================================

func runAppServer() {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	// Seed some initial data
	seedData()

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/stats", statsHandler)
	http.HandleFunc("/user/", userHandler)           // User profile cache
	http.HandleFunc("/product/", productHandler)     // Product catalog cache
	http.HandleFunc("/session/", sessionHandler)     // Session management
	http.HandleFunc("/rate-limit/", rateLimitHandler) // Rate limiting
	http.HandleFunc("/leaderboard", leaderboardHandler) // Leaderboard
	http.HandleFunc("/counter/", counterHandler)     // Distributed counter

	log.Printf("Demo app listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func seedData() {
	ctx := context.Background()

	// Seed some user data
	for i := 1; i <= 100; i++ {
		key := fmt.Sprintf("user:%d", i)
		data := map[string]interface{}{
			"id":       i,
			"name":     fmt.Sprintf("User %d", i),
			"email":    fmt.Sprintf("user%d@example.com", i),
			"created":  time.Now().Add(-time.Duration(rand.Intn(365)) * 24 * time.Hour).Format(time.RFC3339),
		}
		jsonData, _ := json.Marshal(data)
		redisClient.Set(ctx, key, jsonData, 1*time.Hour)
	}

	// Seed product catalog
	for i := 1; i <= 50; i++ {
		key := fmt.Sprintf("product:%d", i)
		data := map[string]interface{}{
			"id":          i,
			"name":        fmt.Sprintf("Product %d", i),
			"price":       rand.Float64() * 100,
			"stock":       rand.Intn(1000),
			"category":    []string{"electronics", "clothing", "books", "home"}[rand.Intn(4)],
		}
		jsonData, _ := json.Marshal(data)
		redisClient.Set(ctx, key, jsonData, 30*time.Minute)
	}

	// Initialize leaderboard
	for i := 1; i <= 100; i++ {
		redisClient.ZAdd(ctx, "leaderboard:global", redis.Z{
			Score:  float64(rand.Intn(10000)),
			Member: fmt.Sprintf("player_%d", i),
		})
	}

	log.Println("Seeded initial data")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		http.Error(w, "Redis unhealthy", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("OK"))
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get Redis info
	ctx := r.Context()
	info, _ := redisClient.Info(ctx, "stats", "memory", "clients").Result()

	response := map[string]interface{}{
		"app_stats":  stats,
		"redis_info": info,
	}
	json.NewEncoder(w).Encode(response)
}

func userHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	userID := r.URL.Path[len("/user/"):]
	key := fmt.Sprintf("user:%s", userID)

	// Try cache
	val, err := redisClient.Get(ctx, key).Result()
	if err == redis.Nil {
		// Cache miss - simulate DB fetch
		time.Sleep(time.Duration(50+rand.Intn(50)) * time.Millisecond)

		data := map[string]interface{}{
			"id":      userID,
			"name":    fmt.Sprintf("User %s", userID),
			"email":   fmt.Sprintf("user%s@example.com", userID),
			"fetched": "from_database",
		}
		jsonData, _ := json.Marshal(data)

		// Cache the result
		redisClient.Set(ctx, key, jsonData, 1*time.Hour)

		stats.RecordRequest(false, time.Since(start).Milliseconds(), false)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "MISS")
		w.Write(jsonData)
		return
	} else if err != nil {
		stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
		http.Error(w, "Redis error", http.StatusInternalServerError)
		return
	}

	stats.RecordRequest(true, time.Since(start).Milliseconds(), false)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "HIT")
	w.Write([]byte(val))
}

func productHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	productID := r.URL.Path[len("/product/"):]
	key := fmt.Sprintf("product:%s", productID)

	val, err := redisClient.Get(ctx, key).Result()
	if err == redis.Nil {
		// Cache miss
		time.Sleep(time.Duration(30+rand.Intn(30)) * time.Millisecond)

		data := map[string]interface{}{
			"id":      productID,
			"name":    fmt.Sprintf("Product %s", productID),
			"price":   rand.Float64() * 100,
			"fetched": "from_database",
		}
		jsonData, _ := json.Marshal(data)
		redisClient.Set(ctx, key, jsonData, 30*time.Minute)

		stats.RecordRequest(false, time.Since(start).Milliseconds(), false)
		w.Header().Set("X-Cache", "MISS")
		w.Write(jsonData)
		return
	} else if err != nil {
		stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
		http.Error(w, "Redis error", http.StatusInternalServerError)
		return
	}

	stats.RecordRequest(true, time.Since(start).Milliseconds(), false)
	w.Header().Set("X-Cache", "HIT")
	w.Write([]byte(val))
}

func sessionHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	sessionID := r.URL.Path[len("/session/"):]
	key := fmt.Sprintf("session:%s", sessionID)

	switch r.Method {
	case "GET":
		val, err := redisClient.Get(ctx, key).Result()
		if err == redis.Nil {
			stats.RecordRequest(false, time.Since(start).Milliseconds(), false)
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		} else if err != nil {
			stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
			http.Error(w, "Redis error", http.StatusInternalServerError)
			return
		}
		stats.RecordRequest(true, time.Since(start).Milliseconds(), false)
		w.Write([]byte(val))

	case "POST":
		data := map[string]interface{}{
			"session_id": sessionID,
			"user_id":    rand.Intn(1000),
			"created":    time.Now().Format(time.RFC3339),
			"expires":    time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		}
		jsonData, _ := json.Marshal(data)

		err := redisClient.Set(ctx, key, jsonData, 24*time.Hour).Err()
		if err != nil {
			stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}
		stats.RecordRequest(false, time.Since(start).Milliseconds(), false)
		w.WriteHeader(http.StatusCreated)
		w.Write(jsonData)

	case "DELETE":
		redisClient.Del(ctx, key)
		stats.RecordRequest(false, time.Since(start).Milliseconds(), false)
		w.WriteHeader(http.StatusNoContent)
	}
}

func rateLimitHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	clientID := r.URL.Path[len("/rate-limit/"):]
	key := fmt.Sprintf("ratelimit:%s", clientID)

	// Simple sliding window rate limiter
	limit := 100 // requests per minute

	count, err := redisClient.Incr(ctx, key).Result()
	if err != nil {
		stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
		http.Error(w, "Redis error", http.StatusInternalServerError)
		return
	}

	if count == 1 {
		redisClient.Expire(ctx, key, time.Minute)
	}

	stats.RecordRequest(true, time.Since(start).Milliseconds(), false)

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(max(0, limit-int(count))))

	if count > int64(limit) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	w.Write([]byte(fmt.Sprintf("OK (requests: %d/%d)", count, limit)))
}

func leaderboardHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	switch r.Method {
	case "GET":
		// Get top 10
		results, err := redisClient.ZRevRangeWithScores(ctx, "leaderboard:global", 0, 9).Result()
		if err != nil {
			stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
			http.Error(w, "Redis error", http.StatusInternalServerError)
			return
		}

		var leaderboard []map[string]interface{}
		for i, z := range results {
			leaderboard = append(leaderboard, map[string]interface{}{
				"rank":   i + 1,
				"player": z.Member,
				"score":  z.Score,
			})
		}

		stats.RecordRequest(true, time.Since(start).Milliseconds(), false)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(leaderboard)

	case "POST":
		player := r.URL.Query().Get("player")
		scoreStr := r.URL.Query().Get("score")
		score, _ := strconv.ParseFloat(scoreStr, 64)

		if player == "" {
			player = fmt.Sprintf("player_%d", rand.Intn(1000))
			score = float64(rand.Intn(1000))
		}

		err := redisClient.ZAdd(ctx, "leaderboard:global", redis.Z{
			Score:  score,
			Member: player,
		}).Err()

		if err != nil {
			stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
			http.Error(w, "Redis error", http.StatusInternalServerError)
			return
		}

		stats.RecordRequest(false, time.Since(start).Milliseconds(), false)
		w.Write([]byte(fmt.Sprintf("Added %s with score %.0f", player, score)))
	}
}

func counterHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	counterName := r.URL.Path[len("/counter/"):]
	key := fmt.Sprintf("counter:%s", counterName)

	val, err := redisClient.Incr(ctx, key).Result()
	if err != nil {
		stats.RecordRequest(false, time.Since(start).Milliseconds(), true)
		http.Error(w, "Redis error", http.StatusInternalServerError)
		return
	}

	stats.RecordRequest(true, time.Since(start).Milliseconds(), false)
	w.Write([]byte(fmt.Sprintf("%d", val)))
}

// ============================================
// LOAD GENERATOR MODE
// ============================================

func runLoadGenerator() {
	targetURL := os.Getenv("TARGET_URL")
	if targetURL == "" {
		targetURL = "http://localhost:8080"
	}

	rpsStr := os.Getenv("REQUESTS_PER_SECOND")
	rps := 50
	if rpsStr != "" {
		rps, _ = strconv.Atoi(rpsStr)
	}

	log.Printf("Load generator starting: target=%s, rps=%d", targetURL, rps)

	// Wait for app to be ready
	for i := 0; i < 30; i++ {
		resp, err := http.Get(targetURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		time.Sleep(time.Second)
	}

	ticker := time.NewTicker(time.Second / time.Duration(rps))
	defer ticker.Stop()

	endpoints := []string{
		"/user/%d",
		"/product/%d",
		"/session/%s",
		"/rate-limit/client-%d",
		"/leaderboard",
		"/counter/pageviews",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for range ticker.C {
		go func() {
			endpoint := endpoints[rand.Intn(len(endpoints))]
			var url string

			switch {
			case endpoint == "/leaderboard":
				url = targetURL + endpoint
			case endpoint == "/counter/pageviews":
				url = targetURL + endpoint
			default:
				url = targetURL + fmt.Sprintf(endpoint, rand.Intn(100)+1)
			}

			start := time.Now()
			resp, err := client.Get(url)
			latency := time.Since(start).Milliseconds()

			if err != nil {
				log.Printf("ERROR: %s - %v (%dms)", url, err, latency)
				return
			}
			resp.Body.Close()

			cacheStatus := resp.Header.Get("X-Cache")
			if cacheStatus == "" {
				cacheStatus = "-"
			}

			log.Printf("%s %s - %d [cache:%s] (%dms)",
				resp.Request.Method, url, resp.StatusCode, cacheStatus, latency)
		}()
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
