package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/gorilla/mux"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

var (
	dockerClient    *client.Client
	agentAuthToken  string
	allowNoAuth     bool
	allowedCORSHost string
)

func main() {
	var err error
	dockerClient, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer dockerClient.Close()

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = dockerClient.Ping(ctx)
	if err != nil {
		log.Fatalf("Failed to connect to Docker daemon: %v", err)
	}
	log.Println("Successfully connected to Docker daemon")

	agentAuthToken = strings.TrimSpace(os.Getenv("AGENT_AUTH_TOKEN"))
	allowNoAuth = strings.EqualFold(os.Getenv("AGENT_ALLOW_NO_AUTH"), "true")
	allowedCORSHost = strings.TrimSpace(os.Getenv("AGENT_ALLOWED_ORIGIN"))

	if agentAuthToken == "" && !allowNoAuth {
		log.Fatal("AGENT_AUTH_TOKEN must be set (or set AGENT_ALLOW_NO_AUTH=true for explicit insecure mode)")
	}
	if agentAuthToken == "" && allowNoAuth {
		log.Println("WARNING: Agent auth disabled (AGENT_ALLOW_NO_AUTH=true). This is insecure for network-exposed deployments.")
	}

	router := mux.NewRouter()

	// Container endpoints
	router.HandleFunc("/containers/json", listContainers).Methods("GET")
	router.HandleFunc("/containers/{id}/json", inspectContainer).Methods("GET")
	router.HandleFunc("/containers/{id}/start", startContainer).Methods("POST")
	router.HandleFunc("/containers/{id}/stop", stopContainer).Methods("POST")
	router.HandleFunc("/containers/{id}/restart", restartContainer).Methods("POST")
	router.HandleFunc("/containers/{id}/rename", renameContainer).Methods("POST")
	router.HandleFunc("/containers/{id}", removeContainer).Methods("DELETE")
	router.HandleFunc("/containers/{id}/logs", getContainerLogs).Methods("GET")
	router.HandleFunc("/containers/{id}/stats", getContainerStats).Methods("GET")
	router.HandleFunc("/containers/create", createContainer).Methods("POST")

	// Image endpoints
	router.HandleFunc("/images/json", listImages).Methods("GET")
	router.HandleFunc("/images/create", pullImage).Methods("POST")
	router.HandleFunc("/images/{id}", removeImage).Methods("DELETE")

	// System endpoints
	router.HandleFunc("/version", getVersion).Methods("GET")
	router.HandleFunc("/info", getInfo).Methods("GET")
	router.HandleFunc("/networks", listNetworks).Methods("GET")
	router.HandleFunc("/volumes", listVolumes).Methods("GET")

	// Agent-specific endpoints (enhanced system stats)
	router.HandleFunc("/agent/stats", getSystemStats).Methods("GET")
	router.HandleFunc("/agent/health", healthCheck).Methods("GET")

	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = "9876"
	}

	log.Printf("Docker Agent starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, corsMiddleware(authMiddleware(router))))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedCORSHost != "" && origin == allowedCORSHost {
			w.Header().Set("Access-Control-Allow-Origin", allowedCORSHost)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Agent-Token")
		if r.Method == "OPTIONS" {
			if allowedCORSHost != "" && origin != "" && origin != allowedCORSHost {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoint is intentionally left unauthenticated for local health checks.
		if r.URL.Path == "/agent/health" || agentAuthToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		providedToken := ""
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			providedToken = strings.TrimSpace(authHeader[7:])
		} else {
			providedToken = strings.TrimSpace(r.Header.Get("X-Agent-Token"))
		}

		if providedToken == "" || subtle.ConstantTimeCompare([]byte(providedToken), []byte(agentAuthToken)) != 1 {
			errorResponse(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// Container handlers

func listContainers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	all := r.URL.Query().Get("all") == "true"
	containers, err := dockerClient.ContainerList(ctx, types.ContainerListOptions{All: all})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, containers)
}

func inspectContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	containerJSON, err := dockerClient.ContainerInspect(ctx, id)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, containerJSON)
}

func startContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	err := dockerClient.ContainerStart(ctx, id, types.ContainerStartOptions{})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func stopContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	err := dockerClient.ContainerStop(ctx, id, container.StopOptions{})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func restartContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	err := dockerClient.ContainerRestart(ctx, id, container.StopOptions{})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func renameContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		errorResponse(w, "missing required query parameter: name", http.StatusBadRequest)
		return
	}

	err := dockerClient.ContainerRename(ctx, id, name)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func removeContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	force := r.URL.Query().Get("force") == "true"
	err := dockerClient.ContainerRemove(ctx, id, types.ContainerRemoveOptions{Force: force})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func getContainerLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}

	options := types.ContainerLogsOptions{
		ShowStdout: r.URL.Query().Get("stdout") != "false",
		ShowStderr: r.URL.Query().Get("stderr") != "false",
		Tail:       tail,
		Timestamps: r.URL.Query().Get("timestamps") == "true",
	}

	logs, err := dockerClient.ContainerLogs(ctx, id, options)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer logs.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, logs)
}

func getContainerStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	stream := r.URL.Query().Get("stream") != "false"

	stats, err := dockerClient.ContainerStats(ctx, id, stream)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stats.Body.Close()

	if stream {
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, stats.Body)
	} else {
		// For non-streaming, read and return single stats snapshot
		var statsJSON types.StatsJSON
		decoder := json.NewDecoder(stats.Body)
		if err := decoder.Decode(&statsJSON); err != nil {
			errorResponse(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, statsJSON)
	}
}

func createContainer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var config struct {
		container.Config
		HostConfig       container.HostConfig     `json:"HostConfig"`
		NetworkingConfig network.NetworkingConfig `json:"NetworkingConfig"`
		Name             string                   `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		errorResponse(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = config.Name // Backward compatibility for older clients.
	}

	resp, err := dockerClient.ContainerCreate(ctx, &config.Config, &config.HostConfig, &config.NetworkingConfig, nil, name)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, resp)
}

// Image handlers

func listImages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	images, err := dockerClient.ImageList(ctx, types.ImageListOptions{All: true})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, images)
}

func pullImage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	imageName := r.URL.Query().Get("fromImage")
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		tag = "latest"
	}

	fullImage := imageName
	if !strings.Contains(imageName, ":") {
		fullImage = imageName + ":" + tag
	}

	reader, err := dockerClient.ImagePull(ctx, fullImage, types.ImagePullOptions{})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Stream progress to client
	io.Copy(w, reader)
}

func removeImage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id := mux.Vars(r)["id"]
	force := r.URL.Query().Get("force") == "true"

	_, err := dockerClient.ImageRemove(ctx, id, types.ImageRemoveOptions{Force: force})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// System handlers

func getVersion(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	version, err := dockerClient.ServerVersion(ctx)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, version)
}

func getInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	info, err := dockerClient.Info(ctx)
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, info)
}

func listNetworks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	networks, err := dockerClient.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, networks)
}

func listVolumes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	volumes, err := dockerClient.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		errorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, volumes)
}

// Agent-specific handlers for enhanced system stats

type SystemStats struct {
	Timestamp  time.Time       `json:"timestamp"`
	CPU        CPUStats        `json:"cpu"`
	Memory     MemoryStats     `json:"memory"`
	Disk       DiskStats       `json:"disk"`
	Docker     DockerStats     `json:"docker"`
	HostInfo   HostInfo        `json:"host"`
	HostSystem HostSystemStats `json:"host_system"` // Actual host system metrics
}

type CPUStats struct {
	UsagePercent float64   `json:"usage_percent"`
	Cores        int       `json:"cores"`
	PerCore      []float64 `json:"per_core,omitempty"`
}

type MemoryStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"used_percent"`
}

type DiskStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
	Path        string  `json:"path"`
}

type DockerStats struct {
	ContainersRunning int `json:"containers_running"`
	ContainersPaused  int `json:"containers_paused"`
	ContainersStopped int `json:"containers_stopped"`
	ImagesTotal       int `json:"images_total"`
}

type HostInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
}

// HostSystemStats contains actual host system metrics (not container limits)
type HostSystemStats struct {
	Memory   HostMemoryStats `json:"memory"`
	Disk     HostDiskStats   `json:"disk"`
	CPU      HostCPUStats    `json:"cpu"`
	Hostname string          `json:"hostname"`
}

type HostMemoryStats struct {
	Total       uint64  `json:"total"`
	Available   uint64  `json:"available"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_percent"`
}

type HostDiskStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type HostCPUStats struct {
	Cores        int     `json:"cores"`
	UsagePercent float64 `json:"usage_percent"`
}

// readCPUStats reads CPU times from /proc/stat
// Returns: user, nice, system, idle, iowait, irq, softirq, steal (all as uint64)
func readCPUStats(path string) (uint64, uint64, uint64, uint64, uint64, uint64, uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) >= 8 {
				user, _ := strconv.ParseUint(fields[1], 10, 64)
				nice, _ := strconv.ParseUint(fields[2], 10, 64)
				system, _ := strconv.ParseUint(fields[3], 10, 64)
				idle, _ := strconv.ParseUint(fields[4], 10, 64)
				iowait, _ := strconv.ParseUint(fields[5], 10, 64)
				irq, _ := strconv.ParseUint(fields[6], 10, 64)
				softirq, _ := strconv.ParseUint(fields[7], 10, 64)
				steal := uint64(0)
				if len(fields) >= 9 {
					steal, _ = strconv.ParseUint(fields[8], 10, 64)
				}
				return user, nice, system, idle, iowait, irq, softirq, steal, nil
			}
		}
	}
	return 0, 0, 0, 0, 0, 0, 0, 0, scanner.Err()
}

// readHostCPUBusy calculates CPU busy percentage from /host/proc/stat
// This measures the busy state of all CPU cores together (like Grafana)
// Returns -1 if unable to read
func readHostCPUBusy() float64 {
	// First sample
	user1, nice1, system1, idle1, iowait1, irq1, softirq1, steal1, err := readCPUStats("/host/proc/stat")
	if err != nil {
		return -1
	}

	// Wait 500ms for second sample
	time.Sleep(500 * time.Millisecond)

	// Second sample
	user2, nice2, system2, idle2, iowait2, irq2, softirq2, steal2, err := readCPUStats("/host/proc/stat")
	if err != nil {
		return -1
	}

	// Calculate deltas
	userDelta := user2 - user1
	niceDelta := nice2 - nice1
	systemDelta := system2 - system1
	idleDelta := idle2 - idle1
	iowaitDelta := iowait2 - iowait1
	irqDelta := irq2 - irq1
	softirqDelta := softirq2 - softirq1
	stealDelta := steal2 - steal1

	// Total CPU time = all states
	totalDelta := userDelta + niceDelta + systemDelta + idleDelta + iowaitDelta + irqDelta + softirqDelta + stealDelta

	// Idle time includes idle + iowait
	idleTotal := idleDelta + iowaitDelta

	// Busy percentage = (total - idle) / total * 100
	if totalDelta == 0 {
		return 0
	}

	busyPercent := float64(totalDelta-idleTotal) / float64(totalDelta) * 100
	return busyPercent
}

// getHostSystemStats reads actual host system metrics from /host mount
func getHostSystemStats() HostSystemStats {
	stats := HostSystemStats{}

	// Read host memory from /host/proc/meminfo
	if file, err := os.Open("/host/proc/meminfo"); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		memInfo := make(map[string]uint64)

		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				key := strings.TrimSuffix(fields[0], ":")
				if val, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					// Values in /proc/meminfo are in kB
					memInfo[key] = val * 1024
				}
			}
		}

		stats.Memory.Total = memInfo["MemTotal"]
		stats.Memory.Available = memInfo["MemAvailable"]
		if stats.Memory.Available == 0 {
			// Fallback for older kernels
			stats.Memory.Available = memInfo["MemFree"] + memInfo["Buffers"] + memInfo["Cached"]
		}
		stats.Memory.Used = stats.Memory.Total - stats.Memory.Available
		if stats.Memory.Total > 0 {
			stats.Memory.UsedPercent = float64(stats.Memory.Used) / float64(stats.Memory.Total) * 100
		}
	}

	// Read host disk usage from /host filesystem
	if diskInfo, err := disk.Usage("/host"); err == nil {
		stats.Disk.Total = diskInfo.Total
		stats.Disk.Used = diskInfo.Used
		stats.Disk.Free = diskInfo.Free
		stats.Disk.UsedPercent = diskInfo.UsedPercent
	}

	// Read host CPU count from /host/proc/cpuinfo
	if file, err := os.Open("/host/proc/cpuinfo"); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		cpuCount := 0
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "processor") {
				cpuCount++
			}
		}
		if cpuCount > 0 {
			stats.CPU.Cores = cpuCount
		} else {
			stats.CPU.Cores = runtime.NumCPU()
		}
	} else {
		stats.CPU.Cores = runtime.NumCPU()
	}

	// Read host CPU usage from /host/proc/stat (Grafana-style: busy state of all cores)
	// Takes two samples 500ms apart to calculate current CPU busy percentage
	cpuBusy := readHostCPUBusy()
	if cpuBusy >= 0 {
		stats.CPU.UsagePercent = cpuBusy
	} else {
		// Fallback to gopsutil if /host/proc/stat not available
		if cpuPercent, err := cpu.Percent(time.Second, false); err == nil && len(cpuPercent) > 0 {
			stats.CPU.UsagePercent = cpuPercent[0]
		}
	}

	// Read actual hostname from /host/etc/hostname
	if data, err := os.ReadFile("/host/etc/hostname"); err == nil {
		stats.Hostname = strings.TrimSpace(string(data))
	} else {
		hostname, _ := os.Hostname()
		stats.Hostname = hostname
	}

	return stats
}

func getSystemStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stats := SystemStats{
		Timestamp: time.Now(),
	}

	// CPU stats
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err == nil && len(cpuPercent) > 0 {
		stats.CPU.UsagePercent = cpuPercent[0]
	}
	stats.CPU.Cores = runtime.NumCPU()

	perCore, err := cpu.Percent(time.Second, true)
	if err == nil {
		stats.CPU.PerCore = perCore
	}

	// Memory stats
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		stats.Memory.Total = memInfo.Total
		stats.Memory.Used = memInfo.Used
		stats.Memory.Available = memInfo.Available
		stats.Memory.UsedPercent = memInfo.UsedPercent
	}

	// Disk stats - try Docker data root first, then fall back to root filesystem
	dockerInfo, _ := dockerClient.Info(ctx)
	diskPath := "/"
	if dockerInfo.DockerRootDir != "" {
		diskPath = dockerInfo.DockerRootDir
	}

	diskInfo, err := disk.Usage(diskPath)
	if err == nil {
		stats.Disk.Total = diskInfo.Total
		stats.Disk.Used = diskInfo.Used
		stats.Disk.Free = diskInfo.Free
		stats.Disk.UsedPercent = diskInfo.UsedPercent
		stats.Disk.Path = diskPath
	} else {
		// Fallback to root
		diskInfo, err = disk.Usage("/")
		if err == nil {
			stats.Disk.Total = diskInfo.Total
			stats.Disk.Used = diskInfo.Used
			stats.Disk.Free = diskInfo.Free
			stats.Disk.UsedPercent = diskInfo.UsedPercent
			stats.Disk.Path = "/"
		}
	}

	// Docker container/image stats
	stats.Docker.ContainersRunning = dockerInfo.ContainersRunning
	stats.Docker.ContainersPaused = dockerInfo.ContainersPaused
	stats.Docker.ContainersStopped = dockerInfo.ContainersStopped
	stats.Docker.ImagesTotal = dockerInfo.Images

	// Host info (container view)
	stats.HostInfo.OS = runtime.GOOS
	stats.HostInfo.Arch = runtime.GOARCH
	hostname, _ := os.Hostname()
	stats.HostInfo.Hostname = hostname

	// Actual host system metrics (from /host mount)
	stats.HostSystem = getHostSystemStats()

	jsonResponse(w, stats)
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := dockerClient.Ping(ctx)
	if err != nil {
		errorResponse(w, "Docker daemon unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now(),
		"docker":    "connected",
	})
}
