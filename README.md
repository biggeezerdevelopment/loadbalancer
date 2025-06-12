# QuantumFlow Load Balancer

<img src="quantomflow.png" alt="QuantumFlow Logo" height="200"/>

A high-performance, feature-rich http/https load balancer written in Go, supporting multiple load balancing strategies, connection pooling, circuit breakers, caching, comprehensive metrics and management dashboard.

## Features

- **Multiple Load Balancing Strategies**
  - Round Robin (default)
  - Weighted Round Robin (with capacity-based weight adjustment)
  - IP Hash (with configurable headers)
  - Least Connections (based on active connection count)

- **Connection Pooling**
  - Configurable pool sizes
  - Connection timeouts
  - Keep-alive settings
  - TLS handshake timeouts
  - Compression control

- **Circuit Breaker Pattern**
  - Configurable failure thresholds
  - Success thresholds for recovery
  - Half-open state for gradual recovery
  - Automatic backend isolation

- **Response Caching**
  - Configurable TTL
  - Header-based cache keys
  - Path-based exclusions
  - LRU eviction policy
  - Cache size limits

- **Comprehensive Metrics**
  - Prometheus integration
  - Request counts and durations
  - Backend health and performance
  - Cache hit/miss statistics
  - Circuit breaker states
  - Active connections tracking

- **Security Features**
  - TLS configuration supports modern cipher suites and protocols
  - Circuit breakers prevent cascading failures
  - Rate limiting and connection pooling protect against DoS
  - Configurable client authentication
  - Secure defaults for all settings

- **High Availability**
  - Health checks
  - Automatic backend recovery
  - Graceful shutdown
  - Worker pool for concurrent request handling

- **Structured Logging**
  - JSON and text formats
  - Log rotation
  - Configurable log levels
  - Request tracing

- **Web-based Management Interface**
  - Real-time configuration updates
  - Backend server management
  - Live metrics visualization
  - Health status monitoring
  - Load balancing strategy configuration
  - Worker pool management

## Prerequisites

- Go 1.22 or later
- OpenSSL (for TLS certificate generation)

## Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/loadbalancer.git
   cd loadbalancer
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Generate TLS certificates (optional):
   ```bash
   mkdir -p certs
   openssl req -x509 -newkey rsa:4096 -keyout certs/key.pem -out certs/cert.pem -days 365 -nodes
   ```

## Usage

1. Start the load balancer and management interface:
   ```bash
   go run main.go
   ```

2. Access the management interface:
   - Open your browser and navigate to `http://localhost:8081`
   - The interface provides:
     - Backend server management
     - Load balancing strategy configuration
     - Worker pool settings
     - Health status monitoring

3. Monitor metrics:
   ```bash
   # Using curl
   curl http://localhost:9091/metrics

   # Using Prometheus
   prometheus --config.file=prometheus.yml
   ```

4. Check logs:
   ```bash
   tail -f logs/loadbalancer.log
   ```

## Management Interface

The web-based management interface provides a user-friendly way to monitor and configure the load balancer:

### Configuration
- Load balancing strategy selection
- Worker pool size adjustment
- Backend server management
- Health check settings

### Backend Management
- Add/remove backend servers
- Configure server weights
- Set maximum connections
- Monitor health status

### Real-time Updates
- Configuration changes take effect immediately
- No service interruption during updates
- Automatic backend reconfiguration
- Health check status updates

## Configuration

The load balancer is configured using a YAML file (`config.yml`). Here's a complete example:

```yaml
server:
  port: ":8080"
  worker_count: 100
  load_balancing:
    strategy: "least_connections"  # Options: round_robin, weighted_round_robin, ip_hash, least_connections
    ip_hash:
      enabled: true
      header: "X-Forwarded-For"
      fallback_header: "X-Real-IP"
    weighted_round_robin:
      enabled: true
      weight_by_capacity: true
  connection_pool:
    enabled: true
    max_idle_conns: 100
    max_idle_conns_per_host: 10
    max_conns_per_host: 100
    idle_conn_timeout: "90s"
    max_lifetime: "1h"
    keep_alive: "30s"
    dial_timeout: "5s"
    tls_handshake_timeout: "10s"
    expect_continue_timeout: "1s"
    response_header_timeout: "10s"
    disable_keep_alives: false
    disable_compression: false
  cache:
    enabled: true
    ttl: "5m"
    max_size: 1000
    headers:
      - "Accept"
      - "Accept-Language"
    exclude_paths:
      - "/api/dynamic"
      - "/admin"
  circuit_breaker:
    enabled: true
    failure_threshold: 5
    success_threshold: 2
    reset_timeout: "30s"
    half_open_timeout: "5s"

metrics:
  enabled: true
  port: ":9090"
  path: "/metrics"
  auth:
    enabled: false
    bearer_token: ""
  collect_interval: "15s"
  retention_period: "24h"
  labels:
    environment: "production"
    region: "us-east-1"
  prometheus:
    enabled: true
    namespace: "loadbalancer"
    subsystem: "http"
    path: "/metrics"

backends:
  - url: "http://backend1:8080"
    weight: 10
    max_connections: 100
  - url: "http://backend2:8080"
    weight: 5
    max_connections: 50

logging:
  enabled: true
  level: "info"
  file: "logs/loadbalancer.log"
  max_size: 100
  max_backups: 3
  max_age: 28
  compress: true
  format: "json"
```

## Metrics

The load balancer exposes comprehensive metrics in Prometheus format:

- **Request Metrics**
  - Total requests
  - Request duration
  - Status code distribution
  - Error counts

- **Backend Metrics**
  - Health status
  - Request counts
  - Error rates
  - Latency

- **Cache Metrics**
  - Hit/miss ratios
  - Cache size
  - Eviction counts

- **Circuit Breaker Metrics**
  - State changes
  - Failure counts
  - Success rates

- **System Metrics**
  - Active connections
  - Memory usage
  - CPU utilization

## Security

- TLS configuration supports modern cipher suites and protocols
- Circuit breakers prevent cascading failures
- Rate limiting and connection pooling protect against DoS
- Configurable client authentication
- Secure defaults for all settings

## Performance

- Efficient connection pooling
- Response caching for improved latency
- Worker pool for concurrent request handling
- Optimized load balancing algorithms
- Minimal memory footprint

## Contributing

1. Fork the repository
2. Create your feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details. 