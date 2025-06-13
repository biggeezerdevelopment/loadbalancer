// Load configuration from server
async function loadConfig() {
    try {
        const response = await fetch('/api/config');
        if (!response.ok) {
            throw new Error('Failed to load configuration');
        }
        const config = await response.json();
        console.log('Loaded configuration:', config);
        populateForms(config);
    } catch (error) {
        console.error('Error loading configuration:', error);
        showError('Failed to load configuration');
    }
}

// Populate all form fields with configuration values
function populateForms(config) {
    // Server configuration
    if (config.server) {
        document.getElementById('serverPort').value = config.server.port || '';
        document.getElementById('serverWorkerCount').value = config.server.worker_count || '';
        document.getElementById('serverTLSEnabled').checked = config.server.tls?.enabled === 'true';
        document.getElementById('serverTLSCertFile').value = config.server.tls?.cert_file || '';
        document.getElementById('serverTLSKeyFile').value = config.server.tls?.key_file || '';
        document.getElementById('serverTLSMinVersion').value = config.server.tls?.min_version || '';
        document.getElementById('serverTLSCipherSuites').value = config.server.tls?.cipher_suites?.join(',') || '';
        document.getElementById('serverTLSClientAuth').value = config.server.tls?.client_auth || '';
    }

    // Load balancing configuration
    if (config.server?.load_balancing) {
        document.getElementById('lbAlgorithm').value = config.server.load_balancing.strategy || 'round_robin';
        document.getElementById('ipHashEnabled').checked = config.server.load_balancing.ip_hash?.enabled === 'true';
        document.getElementById('ipHashHeader').value = config.server.load_balancing.ip_hash?.header || '';
        document.getElementById('ipHashFallbackHeader').value = config.server.load_balancing.ip_hash?.fallback_header || '';
        document.getElementById('weightedRoundRobinEnabled').checked = config.server.load_balancing.weighted_round_robin?.enabled === 'true';
        document.getElementById('weightByCapacity').checked = config.server.load_balancing.weighted_round_robin?.weight_by_capacity === 'true';
    }

    // Connection pool configuration
    if (config.server?.connection_pool) {
        document.getElementById('poolEnabled').checked = config.server.connection_pool.enabled === 'true';
        document.getElementById('maxIdleConns').value = config.server.connection_pool.max_idle_conns || '';
        document.getElementById('maxIdleConnsPerHost').value = config.server.connection_pool.max_idle_conns_per_host || '';
        document.getElementById('maxConnsPerHost').value = config.server.connection_pool.max_conns_per_host || '';
        document.getElementById('idleConnTimeout').value = config.server.connection_pool.idle_conn_timeout || '';
        document.getElementById('maxLifetime').value = config.server.connection_pool.max_lifetime || '';
        document.getElementById('keepAlive').value = config.server.connection_pool.keep_alive || '';
        document.getElementById('dialTimeout').value = config.server.connection_pool.dial_timeout || '';
        document.getElementById('tlsHandshakeTimeout').value = config.server.connection_pool.tls_handshake_timeout || '';
        document.getElementById('expectContinueTimeout').value = config.server.connection_pool.expect_continue_timeout || '';
        document.getElementById('responseHeaderTimeout').value = config.server.connection_pool.response_header_timeout || '';
        document.getElementById('disableKeepAlives').checked = config.server.connection_pool.disable_keep_alives || false;
        document.getElementById('disableCompression').checked = config.server.connection_pool.disable_compression || false;
    }

    // Cache configuration
    if (config.server?.cache) {
        document.getElementById('cacheEnabled').checked = config.server.cache.enabled === 'true';
        document.getElementById('cacheTTL').value = config.server.cache.ttl || '';
        document.getElementById('cacheMaxSize').value = config.server.cache.max_size || '';
        document.getElementById('cacheHeaders').value = config.server.cache.headers?.join(',') || '';
        document.getElementById('cacheExcludePaths').value = config.server.cache.exclude_paths?.join(',') || '';
    }

    // Circuit breaker configuration
    if (config.server?.circuit_breaker) {
        document.getElementById('circuitBreakerEnabled').checked = config.server.circuit_breaker.enabled === 'true';
        document.getElementById('failureThreshold').value = config.server.circuit_breaker.failure_threshold || '';
        document.getElementById('successThreshold').value = config.server.circuit_breaker.success_threshold || '';
        document.getElementById('resetTimeout').value = config.server.circuit_breaker.reset_timeout || '';
        document.getElementById('halfOpenTimeout').value = config.server.circuit_breaker.half_open_timeout || '';
    }

    // Metrics configuration
    if (config.metrics) {
        document.getElementById('metricsEnabled').checked = config.metrics.enabled === 'true';
        document.getElementById('metricsPort').value = config.metrics.port || '';
        document.getElementById('metricsPath').value = config.metrics.path || '';
        document.getElementById('metricsAuthEnabled').checked = config.metrics.auth?.enabled || false;
        document.getElementById('metricsBearerToken').value = config.metrics.auth?.bearer_token || '';
        document.getElementById('collectInterval').value = config.metrics.collect_interval || '';
        document.getElementById('retentionPeriod').value = config.metrics.retention_period || '';
        document.getElementById('metricsLabels').value = Object.entries(config.metrics.labels || {}).map(([k, v]) => `${k}=${v}`).join(',');
        document.getElementById('prometheusEnabled').checked = config.metrics.prometheus?.enabled === 'true';
        document.getElementById('prometheusNamespace').value = config.metrics.prometheus?.namespace || '';
        document.getElementById('prometheusSubsystem').value = config.metrics.prometheus?.subsystem || '';
        document.getElementById('prometheusPath').value = config.metrics.prometheus?.path || '';
    }

    // Logging configuration
    if (config.logging) {
        document.getElementById('loggingEnabled').checked = config.logging.enabled === 'true';
        document.getElementById('loggingLevel').value = config.logging.level || '';
        document.getElementById('loggingFile').value = config.logging.file || '';
        document.getElementById('loggingMaxSize').value = config.logging.max_size || '';
        document.getElementById('loggingMaxBackups').value = config.logging.max_backups || '';
        document.getElementById('loggingMaxAge').value = config.logging.max_age || '';
        document.getElementById('loggingCompress').checked = config.logging.compress === 'true';
        document.getElementById('loggingFormat').value = config.logging.format || '';
    }

    // Dashboard configuration
    if (config.dashboard) {
        document.getElementById('dashboardPort').value = config.dashboard.port || '';
        document.getElementById('dashboardAuthEnabled').checked = config.dashboard.auth?.enabled === 'true';
        document.getElementById('dashboardUsername').value = config.dashboard.auth?.username || '';
        document.getElementById('dashboardPassword').value = config.dashboard.auth?.password || '';
        document.getElementById('sessionTimeout').value = config.dashboard.auth?.session_timeout || '';
    }
}

// Save configuration to server
async function saveConfig(formId) {
    try {
        const form = document.getElementById(formId);
        const formData = new FormData(form);
        const config = {};

        // Convert form data to nested object structure
        for (const [key, value] of formData.entries()) {
            const keys = key.split('.');
            let current = config;
            for (let i = 0; i < keys.length - 1; i++) {
                if (!current[keys[i]]) {
                    current[keys[i]] = {};
                }
                current = current[keys[i]];
            }
            current[keys[keys.length - 1]] = value;
        }

        const response = await fetch('/api/config', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(config),
        });

        if (!response.ok) {
            throw new Error('Failed to save configuration');
        }

        showSuccess('Configuration saved successfully');
    } catch (error) {
        console.error('Error saving configuration:', error);
        showError('Failed to save configuration');
    }
}

// Show success message
function showSuccess(message) {
    const alert = document.createElement('div');
    alert.className = 'fixed top-4 right-4 bg-green-500 text-white px-6 py-3 rounded shadow-lg';
    alert.textContent = message;
    document.body.appendChild(alert);
    setTimeout(() => alert.remove(), 3000);
}

// Show error message
function showError(message) {
    const alert = document.createElement('div');
    alert.className = 'fixed top-4 right-4 bg-red-500 text-white px-6 py-3 rounded shadow-lg';
    alert.textContent = message;
    document.body.appendChild(alert);
    setTimeout(() => alert.remove(), 3000);
}

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    // Load initial configuration
    loadConfig();

    // Set up form submission handlers
    document.getElementById('serverForm').addEventListener('submit', (e) => {
        e.preventDefault();
        saveConfig('serverForm');
    });

    document.getElementById('loadBalancingForm').addEventListener('submit', (e) => {
        e.preventDefault();
        saveConfig('loadBalancingForm');
    });

    document.getElementById('metricsForm').addEventListener('submit', (e) => {
        e.preventDefault();
        saveConfig('metricsForm');
    });

    document.getElementById('loggingForm').addEventListener('submit', (e) => {
        e.preventDefault();
        saveConfig('loggingForm');
    });

    // Set up navigation
    document.querySelectorAll('.nav-item').forEach(item => {
        item.addEventListener('click', (e) => {
            e.preventDefault();
            const targetId = item.getAttribute('href').substring(1);
            document.querySelectorAll('section').forEach(section => {
                section.classList.add('hidden');
            });
            document.getElementById(targetId).classList.remove('hidden');
            document.querySelectorAll('.nav-item').forEach(navItem => {
                navItem.classList.remove('active');
            });
            item.classList.add('active');
        });
    });

    // Set up logout
    document.getElementById('logoutButton').addEventListener('click', async () => {
        try {
            const response = await fetch('/api/logout', {
                method: 'POST',
            });
            if (response.ok) {
                window.location.href = '/login';
            }
        } catch (error) {
            console.error('Error logging out:', error);
            showError('Failed to log out');
        }
    });

    // Initialize dashboard functionality
    console.log('Dashboard initialized');
}); 