(function() {
    'use strict';

    var credentialsEl = document.getElementById('credentials');
    var loadingEl = document.getElementById('loading');
    var errorEl = document.getElementById('error');
    var statusEl = document.getElementById('status');

    function loadCredentials() {
        loadingEl.style.display = 'block';
        errorEl.style.display = 'none';
        credentialsEl.innerHTML = '';

        fetch('/api/credentials')
            .then(function(resp) {
                if (!resp.ok) throw new Error('Failed to fetch credentials (HTTP ' + resp.status + ')');
                return resp.json();
            })
            .then(function(creds) {
                loadingEl.style.display = 'none';
                statusEl.textContent = 'Connected';
                statusEl.className = 'status-badge connected';

                if (!creds || creds.length === 0) {
                    credentialsEl.innerHTML = '<p style="color:var(--text-muted)">No credentials available.</p>';
                    return;
                }

                creds.forEach(function(cred) {
                    var card = document.createElement('div');
                    card.className = 'credential-card';
                    card.innerHTML =
                        '<div class="username">' + escapeHtml(cred.username) + '</div>' +
                        '<div class="device">' + escapeHtml(cred.device || 'Unknown device') + '</div>' +
                        '<div class="ip">' + escapeHtml(cred.ip) + '</div>' +
                        '<div class="connect-hint">Click to connect</div>';
                    card.onclick = function() {
                        window.location.href = '/terminal.html?username=' +
                            encodeURIComponent(cred.username) +
                            '&ip=' + encodeURIComponent(cred.ip) +
                            '&device=' + encodeURIComponent(cred.device || '');
                    };
                    credentialsEl.appendChild(card);
                });
            })
            .catch(function(err) {
                loadingEl.style.display = 'none';
                errorEl.style.display = 'block';
                errorEl.textContent = err.message;
                statusEl.textContent = 'Error';
                statusEl.className = 'status-badge error';
            });
    }

    function escapeHtml(str) {
        var div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    // Expose for refresh button
    window.loadCredentials = loadCredentials;

    // Load on page ready
    loadCredentials();
})();
