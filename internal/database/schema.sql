-- Configuration tables
CREATE TABLE IF NOT EXISTS tls_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled TEXT NOT NULL DEFAULT 'false',
    cert_file TEXT DEFAULT '',
    key_file TEXT DEFAULT '',
    min_version TEXT DEFAULT '',
    cipher_suites TEXT DEFAULT '',
    client_auth TEXT DEFAULT '',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS load_balancing_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strategy TEXT NOT NULL DEFAULT 'round_robin',
    ip_hash_enabled TEXT DEFAULT 'false',
    ip_hash_header TEXT DEFAULT '',
    ip_hash_fallback_header TEXT DEFAULT '',
    weighted_round_robin_enabled TEXT DEFAULT 'false',
    weighted_round_robin_weight_by_capacity TEXT DEFAULT 'false',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS connection_pool_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled TEXT NOT NULL DEFAULT 'true',
    max_idle_conns TEXT DEFAULT '',
    max_idle_conns_per_host TEXT DEFAULT '',
    max_conns_per_host TEXT DEFAULT '',
    idle_conn_timeout TEXT DEFAULT '',
    max_lifetime TEXT DEFAULT '',
    keep_alive TEXT DEFAULT '',
    dial_timeout TEXT DEFAULT '',
    tls_handshake_timeout TEXT DEFAULT '',
    expect_continue_timeout TEXT DEFAULT '',
    response_header_timeout TEXT DEFAULT '',
    disable_keep_alives INTEGER DEFAULT 0,
    disable_compression INTEGER DEFAULT 0,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS cache_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled TEXT NOT NULL DEFAULT 'false',
    ttl TEXT DEFAULT '',
    max_size TEXT DEFAULT '',
    headers TEXT DEFAULT '',
    methods TEXT DEFAULT '',
    status_codes TEXT DEFAULT '',
    exclude_paths TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS circuit_breaker_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled TEXT NOT NULL DEFAULT 'false',
    failure_threshold TEXT DEFAULT '',
    success_threshold TEXT DEFAULT '',
    reset_timeout TEXT DEFAULT '',
    half_open_timeout TEXT DEFAULT '',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS metrics_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled TEXT NOT NULL DEFAULT 'false',
    port TEXT DEFAULT '',
    path TEXT DEFAULT '',
    auth_enabled INTEGER DEFAULT 0,
    bearer_token TEXT DEFAULT '',
    collect_interval TEXT DEFAULT '',
    retention_period TEXT DEFAULT '',
    labels TEXT DEFAULT '',
    prometheus_enabled TEXT DEFAULT 'false',
    prometheus_namespace TEXT DEFAULT '',
    prometheus_subsystem TEXT DEFAULT '',
    prometheus_path TEXT DEFAULT '',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS logging_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled TEXT NOT NULL DEFAULT 'false',
    level TEXT DEFAULT '',
    file TEXT DEFAULT '',
    max_size TEXT DEFAULT '',
    max_backups TEXT DEFAULT '',
    max_age TEXT DEFAULT '',
    compress TEXT DEFAULT '',
    format TEXT DEFAULT '',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS backends (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    url TEXT NOT NULL DEFAULT '',
    weight TEXT NOT NULL DEFAULT '1',
    max_connections TEXT NOT NULL DEFAULT '100',
    healthy INTEGER DEFAULT 1,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Add server_config table
CREATE TABLE IF NOT EXISTS server_config (
    id INTEGER PRIMARY KEY,
    port TEXT NOT NULL DEFAULT '8080',
    worker_count TEXT NOT NULL DEFAULT '4'
);

-- Insert default configurations
INSERT OR IGNORE INTO tls_config (id, enabled) VALUES (1, 'false');
INSERT OR IGNORE INTO load_balancing_config (id, strategy) VALUES (1, 'round_robin');
INSERT OR IGNORE INTO connection_pool_config (id, enabled) VALUES (1, 'true');
INSERT OR IGNORE INTO cache_config (id, enabled, ttl, max_size, headers, methods, status_codes, exclude_paths) VALUES (1, 'false', '300', '1000', '[]', '[]', '[]', '[]');
INSERT OR IGNORE INTO circuit_breaker_config (id, enabled) VALUES (1, 'false');
INSERT OR IGNORE INTO metrics_config (id, enabled) VALUES (1, 'false');
INSERT OR IGNORE INTO logging_config (id, enabled) VALUES (1, 'false');
INSERT OR IGNORE INTO server_config (id, port, worker_count) VALUES (1, '8080', '100'); 