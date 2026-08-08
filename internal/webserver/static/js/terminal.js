(function() {
    'use strict';

    var params = new URLSearchParams(window.location.search);
    var username = params.get('username');
    var ip = params.get('ip');
    var device = params.get('device') || ip;

    if (!username || !ip) {
        window.location.href = '/';
        return;
    }

    var displayEl = document.getElementById('display');
    var connInfo = document.getElementById('connInfo');
    var statusOverlay = document.getElementById('status-overlay');
    var statusText = document.getElementById('status-text');

    connInfo.textContent = username + '@' + device + ' (' + ip + ')';
    statusOverlay.style.display = 'block';
    statusText.textContent = 'Connecting to ' + username + '@' + ip + '...';

    var toolbarHeight = 42;
    var displayWidth = window.innerWidth;
    var displayHeight = window.innerHeight - toolbarHeight;

    var wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    var wsUrl = wsProtocol + '//' + window.location.host + '/api/ws';
    var connectParams = 'username=' + encodeURIComponent(username) +
        '&ip=' + encodeURIComponent(ip) +
        '&width=' + displayWidth +
        '&height=' + displayHeight;

    var tunnel = new Guacamole.WebSocketTunnel(wsUrl);
    var guac = new Guacamole.Client(tunnel);
    var guacDisplay = guac.getDisplay();
    displayEl.appendChild(guacDisplay.getElement());

    // Remembers a fatal error message so the follow-up "Disconnected" state
    // change doesn't overwrite it (the user must see WHY it failed).
    var connError = null;

    guac.onerror = function(error) {
        connError = (error && error.message) ? error.message : 'Unknown error';
        statusOverlay.style.display = 'block';
        statusText.textContent = 'Connection error: ' + connError;
        statusText.style.color = '#f85149';
    };

    guac.onstatechange = function(state) {
        switch (state) {
            case 1: statusOverlay.style.display = 'block'; statusText.textContent = 'Connecting...'; break;
            case 2: statusText.textContent = 'Waiting for server...'; break;
            case 3:
                statusOverlay.style.display = 'none';
                displayEl.focus();
                // Show Files button — SFTP is handled by backend
                fmBtn.classList.add('visible');
                break;
            case 4:
                if (connError) return; // keep the error visible
                statusOverlay.style.display = 'block'; statusText.textContent = 'Disconnecting...';
                break;
            case 5:
                statusOverlay.style.display = 'block';
                // Preserve a fatal error message instead of replacing it with "Disconnected".
                statusText.textContent = connError ? ('Connection error: ' + connError) : 'Disconnected';
                break;
        }
    };

    // =============================================
    //  SFTP API HELPER
    // =============================================
    function sftpUrl(endpoint, extraParams) {
        var url = '/api/sftp/' + endpoint + '?username=' + encodeURIComponent(username) + '&ip=' + encodeURIComponent(ip);
        if (extraParams) url += '&' + extraParams;
        return url;
    }

    // =============================================
    //  FILE MANAGER STATE
    // =============================================
    var currentPath = '/';
    var currentEntries = [];      // [{name, fullPath, isDir, size, modTime, mode}]
    var selectedFiles = [];
    var lastClickedIndex = -1;
    var fmOpen = false;
    var fmFullscreen = false;
    var sortField = 'name';
    var sortAsc = true;
    var viewedFilePath = null;
    var viewedFileName = null;

    // DOM refs
    var fmPanel = document.getElementById('file-manager');
    var fmBtn = document.getElementById('file-manager-btn');
    var fmBreadcrumb = document.getElementById('fm-breadcrumb');
    var fmList = document.getElementById('fm-list');
    var fmProgress = document.getElementById('fm-progress');
    var fmFileInput = document.getElementById('fm-file-input');
    var fmDirInput = document.getElementById('fm-dir-input');
    var fmDropOverlay = document.getElementById('fm-drop-overlay');
    var fmStatusInfo = document.getElementById('fm-status-info');
    var fmStatusSel = document.getElementById('fm-status-sel');
    var fmSelectAllCb = document.getElementById('fm-select-all-cb');
    var fmTbDownload = document.getElementById('fm-tb-download');
    var fmTbView = document.getElementById('fm-tb-view');
    var fmTbClear = document.getElementById('fm-tb-clear');
    var fmCtx = document.getElementById('fm-ctx');
    var fileViewer = document.getElementById('file-viewer');
    var fvFilename = document.getElementById('fv-filename');

    var FM_WIDTH = 420;

    // Binary extensions that should NOT be opened in the viewer
    var BINARY_EXTENSIONS = [
        'zip','tar','gz','bz2','xz','7z','rar','deb','rpm','iso',
        'jpg','jpeg','png','gif','bmp','svg','webp','ico','tiff',
        'mp3','wav','flac','ogg','aac','wma',
        'mp4','avi','mkv','mov','webm','flv','wmv',
        'pdf','doc','docx','xls','xlsx','ppt','pptx',
        'exe','bin','so','dylib','dll','o','a','class','jar',
        'db','sqlite','mdb'
    ];

    function isTextFile(name) {
        // All files are viewable EXCEPT known binary formats
        var ext = name.lastIndexOf('.') > 0 ? name.substring(name.lastIndexOf('.') + 1).toLowerCase() : '';
        if (ext === '') return true; // no extension = try as text
        return BINARY_EXTENSIONS.indexOf(ext) === -1;
    }

    function getFileExt(name) {
        var i = name.lastIndexOf('.');
        return i > 0 ? name.substring(i + 1).toLowerCase() : '';
    }

    function getFileIcon(name) {
        var ext = getFileExt(name);
        switch (ext) {
            case 'txt': case 'md': case 'log': case 'csv': return '\uD83D\uDCC4';
            case 'js': case 'ts': case 'py': case 'go': case 'sh': case 'rb': case 'java': case 'c': case 'cpp': case 'h': case 'rs': case 'php': return '\uD83D\uDCDC';
            case 'json': case 'xml': case 'yaml': case 'yml': case 'toml': case 'ini': case 'cfg': case 'conf': return '\u2699\uFE0F';
            case 'zip': case 'tar': case 'gz': case 'bz2': case 'xz': case '7z': case 'rar': case 'deb': case 'rpm': return '\uD83D\uDCE6';
            case 'jpg': case 'jpeg': case 'png': case 'gif': case 'bmp': case 'svg': case 'webp': case 'ico': return '\uD83D\uDDBC\uFE0F';
            case 'pdf': return '\uD83D\uDCD5';
            case 'key': case 'pem': case 'crt': case 'cer': case 'pub': return '\uD83D\uDD10';
            case 'mp3': case 'wav': case 'flac': case 'ogg': case 'aac': return '\uD83C\uDFB5';
            case 'mp4': case 'avi': case 'mkv': case 'mov': case 'webm': return '\uD83C\uDFAC';
            case 'db': case 'sqlite': case 'sql': return '\uD83D\uDDC3\uFE0F';
            case 'exe': case 'bin': case 'so': case 'dylib': case 'dll': return '\u2699\uFE0F';
            default: return '\uD83D\uDCC4';
        }
    }

    function getFileType(name, isDir) {
        if (isDir) return 'Directory';
        var ext = getFileExt(name);
        if (!ext) return 'File';
        return ext.toUpperCase() + ' file';
    }

    // =============================================
    //  PANEL TOGGLE & FULLSCREEN
    // =============================================
    window.toggleFileManager = function() {
        fmOpen = !fmOpen;
        if (fmOpen) {
            fmPanel.classList.add('open');
            document.body.classList.add('fm-open');
            if (currentEntries.length === 0) listDirectory('/');
        } else {
            fmPanel.classList.remove('open');
            document.body.classList.remove('fm-open');
            fmPanel.classList.remove('fm-fullscreen');
            fmFullscreen = false;
        }
        resizeGuac();
    };

    window.toggleFmFullscreen = function() {
        fmFullscreen = !fmFullscreen;
        fmPanel.classList.toggle('fm-fullscreen', fmFullscreen);
        resizeGuac();
    };

    function resizeGuac() {
        setTimeout(function() {
            var w = fmOpen ? (fmFullscreen ? 0 : window.innerWidth - FM_WIDTH) : window.innerWidth;
            guac.sendSize(Math.max(w, 100), window.innerHeight - toolbarHeight);
        }, 300);
    }

    // =============================================
    //  DIRECTORY LISTING (via HTTP API)
    // =============================================
    function listDirectory(path) {
        currentPath = path;
        selectedFiles = [];
        lastClickedIndex = -1;
        updateSelectionUI();
        renderBreadcrumb(path);
        fmList.innerHTML = '<div class="fm-loading"><div class="spinner"></div>Loading...</div>';
        fmStatusInfo.textContent = 'Loading...';

        fetch(sftpUrl('list', 'path=' + encodeURIComponent(path)))
            .then(function(res) { return res.json(); })
            .then(function(data) {
                if (data.error) {
                    fmList.innerHTML = '<div class="fm-empty">Error: ' + escapeHtml(data.error) + '</div>';
                    fmStatusInfo.textContent = 'Error';
                    return;
                }
                processListing(path, data.entries || []);
            })
            .catch(function(err) {
                fmList.innerHTML = '<div class="fm-empty">Connection error: ' + escapeHtml(err.message) + '</div>';
                fmStatusInfo.textContent = 'Error';
            });
    }

    function processListing(path, entries) {
        currentEntries = [];
        for (var i = 0; i < entries.length; i++) {
            var e = entries[i];
            currentEntries.push({
                name: e.name,
                fullPath: e.path,
                isDir: e.isDir,
                size: e.size || 0,
                modTime: e.modTime || '',
                mode: e.mode || ''
            });
        }
        sortEntries();
        renderFileList();
    }

    function sortEntries() {
        currentEntries.sort(function(a, b) {
            if (a.isDir && !b.isDir) return -1;
            if (!a.isDir && b.isDir) return 1;
            var va, vb;
            if (sortField === 'type') {
                va = getFileType(a.name, a.isDir);
                vb = getFileType(b.name, b.isDir);
            } else if (sortField === 'size') {
                return sortAsc ? (a.size - b.size) : (b.size - a.size);
            } else {
                va = a.name.toLowerCase();
                vb = b.name.toLowerCase();
            }
            var cmp = va.localeCompare(vb);
            return sortAsc ? cmp : -cmp;
        });
    }

    window.toggleSort = function(field) {
        if (sortField === field) { sortAsc = !sortAsc; }
        else { sortField = field; sortAsc = true; }
        document.getElementById('fm-sort-icon').textContent = sortAsc ? '\u25BE' : '\u25B4';
        sortEntries();
        renderFileList();
    };

    function renderFileList() {
        fmList.innerHTML = '';
        var dirCount = 0, fileCount = 0;

        if (currentPath !== '/') {
            fmList.appendChild(createRow(null, -1, true));
        }

        currentEntries.forEach(function(entry, idx) {
            if (entry.isDir) dirCount++; else fileCount++;
            fmList.appendChild(createRow(entry, idx, false));
        });

        if (currentEntries.length === 0) {
            fmList.innerHTML = '<div class="fm-empty">Empty directory</div>';
        }
        fmStatusInfo.textContent = dirCount + ' dirs, ' + fileCount + ' files';
        fmSelectAllCb.checked = false;
    }

    // =============================================
    //  FILE ROW CREATION
    // =============================================
    function createRow(entry, idx, isParent) {
        var div = document.createElement('div');
        div.className = 'fm-item';
        div.setAttribute('data-idx', idx);

        if (isParent) {
            div.classList.add('fm-item-dir');
            div.innerHTML = '<span class="fm-icon">\u2B06</span><span class="fm-name fm-name-dir">..</span><span class="fm-type"></span>';
            div.addEventListener('click', function() {
                listDirectory(currentPath.substring(0, currentPath.lastIndexOf('/')) || '/');
            });
            return div;
        }

        var isDir = entry.isDir;
        div.classList.add(isDir ? 'fm-item-dir' : 'fm-item-file');

        var cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.className = 'fm-checkbox';
        cb.addEventListener('click', function(e) { e.stopPropagation(); });
        cb.addEventListener('change', function() { syncSelectionFromCheckbox(entry, idx, div, cb.checked); });
        div.appendChild(cb);

        var icon = document.createElement('span');
        icon.className = 'fm-icon';
        icon.textContent = isDir ? '\uD83D\uDCC1' : getFileIcon(entry.name);
        div.appendChild(icon);

        var nameSpan = document.createElement('span');
        nameSpan.className = 'fm-name' + (isDir ? ' fm-name-dir' : '');
        nameSpan.textContent = entry.name;
        nameSpan.title = entry.fullPath;
        div.appendChild(nameSpan);

        var typeSpan = document.createElement('span');
        typeSpan.className = 'fm-type';
        typeSpan.textContent = getFileType(entry.name, isDir);
        div.appendChild(typeSpan);

        div.addEventListener('click', function(e) {
            if (e.target.classList.contains('fm-checkbox')) return;
            handleRowClick(entry, idx, div, e);
        });

        div.addEventListener('dblclick', function(e) {
            if (e.target.classList.contains('fm-checkbox')) return;
            if (isDir) {
                listDirectory(entry.fullPath);
            } else if (isTextFile(entry.name)) {
                viewFile(entry.fullPath, entry.name);
            }
        });

        div.addEventListener('contextmenu', function(e) {
            e.preventDefault();
            if (!isSelected(entry)) {
                clearSelectionInternal();
                addToSelection(entry, idx, div);
            }
            showContextMenu(e.clientX, e.clientY, entry);
        });

        return div;
    }

    // =============================================
    //  SELECTION LOGIC
    // =============================================
    function isSelected(entry) {
        for (var i = 0; i < selectedFiles.length; i++) {
            if (selectedFiles[i].fullPath === entry.fullPath) return true;
        }
        return false;
    }

    function addToSelection(entry, idx, div) {
        if (!isSelected(entry)) {
            selectedFiles.push(entry);
            div.classList.add('selected');
            var cb = div.querySelector('.fm-checkbox');
            if (cb) cb.checked = true;
        }
        lastClickedIndex = idx;
        updateSelectionUI();
    }

    function removeFromSelection(entry, div) {
        for (var i = 0; i < selectedFiles.length; i++) {
            if (selectedFiles[i].fullPath === entry.fullPath) {
                selectedFiles.splice(i, 1);
                break;
            }
        }
        div.classList.remove('selected');
        var cb = div.querySelector('.fm-checkbox');
        if (cb) cb.checked = false;
        updateSelectionUI();
    }

    function clearSelectionInternal() {
        selectedFiles = [];
        var items = fmList.querySelectorAll('.fm-item.selected');
        for (var i = 0; i < items.length; i++) {
            items[i].classList.remove('selected');
            var cb = items[i].querySelector('.fm-checkbox');
            if (cb) cb.checked = false;
        }
    }

    window.clearSelection = function() {
        clearSelectionInternal();
        lastClickedIndex = -1;
        updateSelectionUI();
    };

    function handleRowClick(entry, idx, div, e) {
        if (e.shiftKey && lastClickedIndex >= 0) {
            var start = Math.min(lastClickedIndex, idx);
            var end = Math.max(lastClickedIndex, idx);
            if (!e.ctrlKey && !e.metaKey) clearSelectionInternal();
            var rows = fmList.querySelectorAll('.fm-item[data-idx]');
            rows.forEach(function(row) {
                var ri = parseInt(row.getAttribute('data-idx'));
                if (ri >= start && ri <= end && ri >= 0) {
                    var ent = currentEntries[ri];
                    if (ent && !isSelected(ent)) {
                        selectedFiles.push(ent);
                        row.classList.add('selected');
                        var cb = row.querySelector('.fm-checkbox');
                        if (cb) cb.checked = true;
                    }
                }
            });
            updateSelectionUI();
        } else if (e.ctrlKey || e.metaKey) {
            if (isSelected(entry)) removeFromSelection(entry, div);
            else addToSelection(entry, idx, div);
        } else {
            clearSelectionInternal();
            addToSelection(entry, idx, div);
        }
    }

    function syncSelectionFromCheckbox(entry, idx, div, checked) {
        if (checked) addToSelection(entry, idx, div);
        else removeFromSelection(entry, div);
    }

    window.selectAll = function() {
        clearSelectionInternal();
        var rows = fmList.querySelectorAll('.fm-item[data-idx]');
        rows.forEach(function(row) {
            var ri = parseInt(row.getAttribute('data-idx'));
            if (ri >= 0 && currentEntries[ri]) {
                selectedFiles.push(currentEntries[ri]);
                row.classList.add('selected');
                var cb = row.querySelector('.fm-checkbox');
                if (cb) cb.checked = true;
            }
        });
        fmSelectAllCb.checked = true;
        updateSelectionUI();
    };

    fmSelectAllCb.addEventListener('change', function() {
        if (fmSelectAllCb.checked) window.selectAll();
        else window.clearSelection();
    });

    function updateSelectionUI() {
        var count = selectedFiles.length;
        var fileCount = 0, dirCount = 0;
        selectedFiles.forEach(function(f) { if (f.isDir) dirCount++; else fileCount++; });

        fmTbDownload.disabled = (fileCount === 0);
        fmTbClear.disabled = (count === 0);
        var canView = (fileCount === 1 && dirCount === 0 && selectedFiles.length === 1 && isTextFile(selectedFiles[0].name));
        fmTbView.disabled = !canView;

        if (count > 0) {
            var parts = [];
            if (dirCount > 0) parts.push(dirCount + ' dir' + (dirCount > 1 ? 's' : ''));
            if (fileCount > 0) parts.push(fileCount + ' file' + (fileCount > 1 ? 's' : ''));
            fmStatusSel.textContent = parts.join(', ') + ' selected';
        } else {
            fmStatusSel.textContent = '';
        }
        fmSelectAllCb.checked = (count > 0 && count === currentEntries.length);
        if (typeof updateFmButtons === 'function') updateFmButtons();
    }

    // =============================================
    //  CONTEXT MENU
    // =============================================
    var ctxTarget = null;

    function showContextMenu(x, y, entry) {
        ctxTarget = entry;
        fmCtx.style.left = x + 'px';
        fmCtx.style.top = y + 'px';
        fmCtx.classList.add('visible');

        var items = fmCtx.querySelectorAll('.fm-ctx-item');
        items.forEach(function(item) {
            var action = item.getAttribute('data-action');
            if (action === 'open') item.style.display = entry.isDir ? '' : 'none';
            if (action === 'view') item.style.display = (!entry.isDir && isTextFile(entry.name)) ? '' : 'none';
            if (action === 'download') item.style.display = entry.isDir ? 'none' : '';
        });

        setTimeout(function() {
            var rect = fmCtx.getBoundingClientRect();
            if (rect.right > window.innerWidth) fmCtx.style.left = (x - rect.width) + 'px';
            if (rect.bottom > window.innerHeight) fmCtx.style.top = (y - rect.height) + 'px';
        }, 0);
    }

    function hideContextMenu() {
        fmCtx.classList.remove('visible');
        ctxTarget = null;
    }

    document.addEventListener('click', hideContextMenu);
    document.addEventListener('contextmenu', function(e) {
        if (!fmPanel.contains(e.target)) hideContextMenu();
    });

    fmCtx.addEventListener('click', function(e) {
        var item = e.target.closest('.fm-ctx-item');
        if (!item) return;
        var action = item.getAttribute('data-action');
        hideContextMenu();

        switch (action) {
            case 'open':
                if (ctxTarget && ctxTarget.isDir) listDirectory(ctxTarget.fullPath);
                break;
            case 'view':
                if (ctxTarget && !ctxTarget.isDir) viewFile(ctxTarget.fullPath, ctxTarget.name);
                break;
            case 'download':
                if (selectedFiles.length > 0) downloadSelected();
                else if (ctxTarget && !ctxTarget.isDir) downloadFile(ctxTarget.fullPath, ctxTarget.name);
                break;
            case 'upload':
                triggerUpload(false);
                break;
            case 'copypath':
                if (ctxTarget) copyToClipboard(ctxTarget.fullPath);
                break;
            case 'copyname':
                if (ctxTarget) copyToClipboard(ctxTarget.name);
                break;
            case 'refresh':
                refreshDirectory();
                break;
            case 'selectall':
                selectAll();
                break;
            case 'newfolder': newFolder(); break;
            case 'newfile':   newFile(); break;
            case 'rename':    renameSelected(); break;
            case 'copy':      copySelection(false); break;
            case 'cut':       copySelection(true); break;
            case 'paste':     pasteClipboard(); break;
            case 'delete':    deleteSelected(); break;
        }
    });

    // =============================================
    //  FILE OPERATIONS (mkdir, new file, rename, copy/cut/paste, delete)
    // =============================================
    var fmClipboard = { srcs: [], move: false };

    function joinPath(dir, name) { return dir === '/' ? '/' + name : dir.replace(/\/+$/, '') + '/' + name; }
    function parentOf(p) { var q = p.replace(/\/+$/, ''); var i = q.lastIndexOf('/'); return i <= 0 ? '/' : q.slice(0, i); }
    function opTargets() { return selectedFiles.length ? selectedFiles.slice() : (ctxTarget ? [ctxTarget] : []); }

    // POST helper with automatic sudo-retry on permission errors.
    function sftpOp(endpoint, body, onDone) {
        function post(sudo) {
            return fetch(sftpUrl(endpoint, sudo ? 'sudo=true' : ''), {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
            }).then(function(resp) { return resp.json().then(function(d) { return { ok: resp.ok, data: d }; }); });
        }
        post(false).then(function(r) {
            if (r.ok) { onDone && onDone(r.data); return; }
            if (r.data && r.data.permissionError) {
                fmConfirm('Permission denied. Retry with sudo?', function() {
                    post(true).then(function(r2) {
                        if (r2.ok) onDone && onDone(r2.data);
                        else showToast('Failed: ' + ((r2.data && r2.data.error) || 'error'));
                    });
                });
            } else {
                showToast('Failed: ' + ((r.data && r.data.error) || 'error'));
            }
        }).catch(function(err) { showToast('Failed: ' + err.message); });
    }

    function newFolder() {
        fmPrompt('New folder', 'Folder name', 'new-folder', function(name) {
            if (!name) return;
            sftpOp('mkdir', { path: joinPath(currentPath, name) }, function() { refreshDirectory(); showToast('Folder created'); });
        });
    }
    function newFile() {
        fmPrompt('New file', 'File name', 'untitled.txt', function(name) {
            if (!name) return;
            sftpOp('newfile', { path: joinPath(currentPath, name) }, function() { refreshDirectory(); showToast('File created'); });
        });
    }
    function renameSelected() {
        var t = ctxTarget || (selectedFiles.length === 1 ? selectedFiles[0] : null);
        if (!t) { showToast('Select a single item to rename'); return; }
        fmPrompt('Rename', 'New name', t.name, function(name) {
            if (!name || name === t.name) return;
            sftpOp('rename', { from: t.fullPath, to: joinPath(parentOf(t.fullPath), name) }, function() { refreshDirectory(); showToast('Renamed'); });
        });
    }
    function copySelection(move) {
        var items = opTargets();
        if (!items.length) { showToast('Nothing selected'); return; }
        fmClipboard = { srcs: items.map(function(i) { return i.fullPath; }), move: !!move };
        updateFmButtons();
        showToast((move ? 'Cut ' : 'Copied ') + items.length + ' item' + (items.length > 1 ? 's' : ''));
    }
    function pasteClipboard() {
        if (!fmClipboard.srcs.length) { showToast('Clipboard is empty'); return; }
        sftpOp('copy', { srcs: fmClipboard.srcs, destDir: currentPath, move: fmClipboard.move }, function() {
            if (fmClipboard.move) fmClipboard = { srcs: [], move: false };
            updateFmButtons(); refreshDirectory(); showToast('Pasted');
        });
    }
    function deleteSelected() {
        var items = opTargets();
        if (!items.length) { showToast('Nothing selected'); return; }
        var label = items.length === 1 ? '"' + items[0].name + '"' : items.length + ' items';
        fmConfirm('Delete ' + label + '? This cannot be undone.', function() {
            sftpOp('delete', { paths: items.map(function(i) { return i.fullPath; }) }, function() { refreshDirectory(); showToast('Deleted'); });
        }, true);
    }

    function updateFmButtons() {
        var hasSel = selectedFiles.length > 0;
        var one = selectedFiles.length === 1;
        var setDis = function(id, dis) { var b = document.getElementById(id); if (b) b.disabled = dis; };
        setDis('fm-tb-rename', !one);
        setDis('fm-tb-copy', !hasSel);
        setDis('fm-tb-cut', !hasSel);
        setDis('fm-tb-delete', !hasSel);
        setDis('fm-tb-paste', fmClipboard.srcs.length === 0);
    }

    window.newFolder = newFolder;
    window.newFile = newFile;
    window.renameSelected = renameSelected;
    window.copySelection = function() { copySelection(false); };
    window.cutSelection = function() { copySelection(true); };
    window.pasteClipboard = pasteClipboard;
    window.deleteSelected = deleteSelected;

    // --- Lightweight modal for prompt / confirm (accessible, no native dialogs) ---
    function fmModal(cfg) {
        var ov = document.getElementById('fm-modal');
        var titleEl = document.getElementById('fm-modal-title');
        var inputEl = document.getElementById('fm-modal-input');
        var okBtn = document.getElementById('fm-modal-ok');
        var cancelBtn = document.getElementById('fm-modal-cancel');
        titleEl.textContent = cfg.title;
        okBtn.textContent = cfg.okLabel || 'OK';
        okBtn.classList.toggle('danger', !!cfg.danger);
        if (cfg.input) {
            inputEl.style.display = '';
            inputEl.value = cfg.value || '';
            inputEl.placeholder = cfg.placeholder || '';
        } else {
            inputEl.style.display = 'none';
        }
        ov.classList.add('open');
        if (cfg.input) { inputEl.focus(); inputEl.select(); } else { okBtn.focus(); }

        function close() { ov.classList.remove('open'); okBtn.onclick = cancelBtn.onclick = inputEl.onkeydown = ov.onmousedown = null; }
        okBtn.onclick = function() { var v = cfg.input ? inputEl.value.trim() : true; close(); cfg.onOk && cfg.onOk(v); };
        cancelBtn.onclick = close;
        ov.onmousedown = function(e) { if (e.target === ov) close(); };
        inputEl.onkeydown = function(e) {
            if (e.key === 'Enter') { e.preventDefault(); okBtn.click(); }
            else if (e.key === 'Escape') { e.preventDefault(); close(); }
            e.stopPropagation();
        };
    }
    function fmPrompt(title, placeholder, value, onOk) {
        fmModal({ title: title, input: true, placeholder: placeholder, value: value, okLabel: 'OK', onOk: onOk });
    }
    function fmConfirm(msg, onOk, danger) {
        fmModal({ title: msg, input: false, okLabel: danger ? 'Delete' : 'Confirm', danger: danger, onOk: function() { onOk(); } });
    }

    fmList.addEventListener('contextmenu', function(e) {
        if (e.target === fmList || e.target.classList.contains('fm-empty')) {
            e.preventDefault();
            ctxTarget = { name: currentPath, fullPath: currentPath, isDir: true };
            showContextMenu(e.clientX, e.clientY, ctxTarget);
        }
    });

    // =============================================
    //  BREADCRUMB
    // =============================================
    function renderBreadcrumb(path) {
        fmBreadcrumb.innerHTML = '';
        var rootBtn = document.createElement('button');
        rootBtn.className = 'fm-breadcrumb-item';
        rootBtn.textContent = '/';
        rootBtn.addEventListener('click', function() { listDirectory('/'); });
        fmBreadcrumb.appendChild(rootBtn);

        if (path === '/') return;
        var parts = path.split('/').filter(function(p) { return p !== ''; });
        var accumulated = '';
        parts.forEach(function(part, index) {
            accumulated += '/' + part;
            var sep = document.createElement('span');
            sep.className = 'fm-breadcrumb-sep';
            sep.textContent = '\u203A';
            fmBreadcrumb.appendChild(sep);

            if (index === parts.length - 1) {
                var current = document.createElement('span');
                current.className = 'fm-breadcrumb-current';
                current.textContent = part;
                fmBreadcrumb.appendChild(current);
            } else {
                var btn = document.createElement('button');
                btn.className = 'fm-breadcrumb-item';
                btn.textContent = part;
                (function(p) { btn.addEventListener('click', function() { listDirectory(p); }); })(accumulated);
                fmBreadcrumb.appendChild(btn);
            }
        });
    }

    // =============================================
    //  FILE VIEWER / EDITOR
    // =============================================
    var viewerRawContent = '';
    var viewerWordWrap = false;
    var viewerSearchOpen = false;
    var viewerSearchMatches = [];
    var viewerSearchCurrent = -1;
    var viewerEditMode = false;
    var viewerModified = false;

    var fvEditor = document.getElementById('fv-editor');
    var fvGutter = document.getElementById('fv-gutter');
    var fvCode = document.getElementById('fv-code');
    var fvFileIcon = document.getElementById('fv-file-icon');
    var fvBadge = document.getElementById('fv-badge');
    var fvInfoEl = document.getElementById('fv-info');
    var fvSearch = document.getElementById('fv-search');
    var fvSearchInput = document.getElementById('fv-search-input');
    var fvSearchCount = document.getElementById('fv-search-count');
    var fvStatusLeft = document.getElementById('fv-status-left');
    var fvStatusRight = document.getElementById('fv-status-right');

    // ---- VIEW FILE via SFTP HTTP API ----
    function viewFile(path, filename) {
        viewedFilePath = path;
        viewedFileName = filename;
        viewerRawContent = '';
        viewerSearchMatches = [];
        viewerSearchCurrent = -1;
        viewerEditMode = false;
        viewerModified = false;

        fileViewer.classList.add('open');
        fvFilename.textContent = filename;
        fvFileIcon.textContent = getFileIcon(filename);
        fvInfoEl.textContent = '';
        fvStatusLeft.textContent = 'Loading...';
        fvStatusRight.textContent = getFileExt(filename).toUpperCase() || 'TEXT';
        fvBadge.textContent = 'READ ONLY';
        fvGutter.innerHTML = '';
        fvCode.innerHTML = '<div class="fv-loading"><div class="spinner"></div>Loading file via SFTP...</div>';
        fvCode.contentEditable = 'false';
        fvCode.classList.remove('fv-editable');

        fvSearch.classList.remove('open');
        viewerSearchOpen = false;
        updateEditBtn();

        fetch(sftpUrl('read', 'path=' + encodeURIComponent(path)))
            .then(function(res) {
                if (!res.ok) return res.json().then(function(d) { throw new Error(d.error || 'Read failed'); });
                fvInfoEl.textContent = formatBytes(parseInt(res.headers.get('X-File-Size') || '0'));
                return res.text();
            })
            .then(function(text) {
                viewerRawContent = text;
                renderEditorContent(text, filename);
            })
            .catch(function(err) {
                fvCode.innerHTML = '<div class="fv-error">Failed to read file:<br>' + escapeHtml(err.message) + '</div>';
                fvGutter.innerHTML = '';
                fvStatusLeft.textContent = 'Error';
            });
    }

    function renderEditorContent(text, filename) {
        var lines = text.split('\n');
        var lineCount = lines.length;
        if (lines[lineCount - 1] === '' && lineCount > 1) lineCount--;

        // Gutter: line numbers
        var gutterHtml = '';
        for (var i = 1; i <= lineCount; i++) {
            gutterHtml += '<span class="fv-line-num">' + i + '</span>';
        }
        fvGutter.innerHTML = gutterHtml;

        // Display raw text as-is, no formatting/highlighting
        fvCode.textContent = text;

        var ext = getFileExt(filename);
        fvStatusLeft.textContent = 'Ln ' + lineCount + ', Col 1';
        fvStatusRight.textContent = (ext.toUpperCase() || 'TEXT') + '  |  UTF-8  |  ' + lineCount + ' lines';

        fvCode.onscroll = function() { fvGutter.scrollTop = fvCode.scrollTop; };
    }

    function escapeHtml(str) {
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    // =============================================
    //  EDIT MODE
    // =============================================
    function updateEditBtn() {
        var btn = document.getElementById('fv-edit-btn');
        if (!btn) return;
        if (viewerEditMode) {
            btn.textContent = '\u270F Edit: ON';
            btn.classList.add('active');
        } else {
            btn.textContent = '\u270F Edit';
            btn.classList.remove('active');
        }
        var saveBtn = document.getElementById('fv-save-btn');
        if (saveBtn) saveBtn.style.display = viewerEditMode ? '' : 'none';
    }

    window.toggleEditMode = function() {
        viewerEditMode = !viewerEditMode;
        if (viewerEditMode) {
            // Switch to edit: render raw content in contentEditable pre
            fvCode.textContent = viewerRawContent;
            fvCode.contentEditable = 'true';
            fvCode.classList.add('fv-editable');
            fvBadge.textContent = 'EDITING';
            fvBadge.style.color = '#ffa657';
            fvBadge.style.background = 'rgba(255,166,87,0.15)';
            fvStatusLeft.textContent = 'Edit mode - type to modify';
            // Update gutter
            updateGutterFromContent();
            fvCode.focus();
            fvCode.addEventListener('input', onEditInput);
        } else {
            // Switch back to view: re-render with syntax highlighting
            fvCode.removeEventListener('input', onEditInput);
            fvCode.contentEditable = 'false';
            fvCode.classList.remove('fv-editable');
            fvBadge.textContent = 'READ ONLY';
            fvBadge.style.color = '';
            fvBadge.style.background = '';
            renderEditorContent(viewerRawContent, viewedFileName);
        }
        updateEditBtn();
    };

    function onEditInput() {
        viewerModified = true;
        viewerRawContent = fvCode.textContent;
        fvBadge.textContent = 'MODIFIED';
        updateGutterFromContent();
    }

    function updateGutterFromContent() {
        var text = fvCode.textContent || '';
        var lineCount = text.split('\n').length;
        var gutterHtml = '';
        for (var i = 1; i <= lineCount; i++) {
            gutterHtml += '<span class="fv-line-num">' + i + '</span>';
        }
        fvGutter.innerHTML = gutterHtml;
        fvStatusLeft.textContent = 'Ln ' + lineCount;
    }

    // ---- SAVE FILE via SFTP HTTP API ----
    function doSaveFile(useSudo) {
        if (!viewedFilePath || !viewerEditMode) return;
        var content = fvCode.textContent;

        fvStatusLeft.textContent = useSudo ? 'Saving with sudo...' : 'Saving...';
        fvBadge.textContent = 'SAVING...';

        var url = sftpUrl('write', 'path=' + encodeURIComponent(viewedFilePath));
        if (useSudo) url += '&sudo=true';

        fetch(url, {
            method: 'PUT',
            headers: { 'Content-Type': 'text/plain' },
            body: content
        })
        .then(function(res) { return res.json().then(function(d) { d._status = res.status; return d; }); })
        .then(function(data) {
            if (data.error) {
                // Permission error: propose sudo retry
                if (data.permissionError && !useSudo) {
                    fvBadge.textContent = 'PERMISSION DENIED';
                    fvBadge.style.color = '#f85149';
                    fvBadge.style.background = 'rgba(248,81,73,0.15)';
                    fvStatusLeft.textContent = 'Permission denied — retry with sudo?';
                    if (confirm('Permission denied.\n\nRetry save with sudo?')) {
                        doSaveFile(true);
                    }
                    return;
                }
                fvStatusLeft.textContent = 'Save FAILED: ' + data.error;
                fvBadge.textContent = 'SAVE ERROR';
                fvBadge.style.color = '#f85149';
                return;
            }
            viewerModified = false;
            viewerRawContent = content;
            fvBadge.textContent = data.sudo ? 'SAVED (sudo)' : 'SAVED';
            fvBadge.style.color = '#3fb950';
            fvBadge.style.background = 'rgba(63,185,80,0.15)';
            fvStatusLeft.textContent = 'Saved (' + formatBytes(data.size) + ')' + (data.sudo ? ' via sudo' : '');
            fvInfoEl.textContent = formatBytes(data.size);
            setTimeout(function() {
                if (viewerEditMode) {
                    fvBadge.textContent = 'EDITING';
                    fvBadge.style.color = '#ffa657';
                    fvBadge.style.background = 'rgba(255,166,87,0.15)';
                }
            }, 2000);
        })
        .catch(function(err) {
            fvStatusLeft.textContent = 'Save error: ' + err.message;
            fvBadge.textContent = 'SAVE ERROR';
        });
    }

    window.saveFile = function() { doSaveFile(false); };

    // =============================================
    //  SYNTAX HIGHLIGHTING
    // =============================================
    function applySyntaxHighlight(line, ext) {
        var lang = getLangFromExt(ext);
        if (lang === 'none') return line;

        if (lang === 'sh' || lang === 'py' || lang === 'yaml' || lang === 'conf') {
            line = line.replace(/^(\s*)(#.*)$/, '$1<span class="syn-comment">$2</span>');
        }
        if (lang === 'js' || lang === 'go' || lang === 'java' || lang === 'c' || lang === 'rust') {
            line = line.replace(/(\/\/.*)$/, '<span class="syn-comment">$1</span>');
        }
        line = line.replace(/(&quot;(?:[^&]|&(?!quot;))*?&quot;)/g, '<span class="syn-string">$1</span>');
        line = line.replace(/(&#39;(?:[^&]|&(?!#39;))*?&#39;)/g, '<span class="syn-string">$1</span>');
        line = line.replace(/('(?:[^'\\]|\\.)*?')/g, '<span class="syn-string">$1</span>');
        line = line.replace(/\b(\d+\.?\d*)\b/g, '<span class="syn-number">$1</span>');

        if (lang === 'js' || lang === 'ts') {
            line = highlightKeywords(line, ['function','var','let','const','return','if','else','for','while','switch','case','break','continue','new','this','class','import','export','default','from','async','await','try','catch','throw','typeof','instanceof','null','undefined','true','false']);
        } else if (lang === 'go') {
            line = highlightKeywords(line, ['func','var','const','return','if','else','for','range','switch','case','break','continue','type','struct','interface','map','package','import','defer','go','chan','select','nil','true','false','string','int','bool','error','byte','fmt']);
        } else if (lang === 'py') {
            line = highlightKeywords(line, ['def','class','return','if','elif','else','for','while','import','from','as','try','except','finally','raise','with','yield','lambda','pass','break','continue','and','or','not','in','is','None','True','False','self','print']);
        } else if (lang === 'sh') {
            line = highlightKeywords(line, ['if','then','else','elif','fi','for','do','done','while','until','case','esac','function','return','exit','echo','export','source','local','readonly','set','unset','eval','exec','cd','pwd','true','false']);
        } else if (lang === 'java' || lang === 'c') {
            line = highlightKeywords(line, ['public','private','protected','static','final','void','int','long','double','float','char','boolean','class','interface','extends','implements','return','if','else','for','while','switch','case','break','continue','new','this','null','true','false','import','package','try','catch','throw','throws','abstract']);
        } else if (lang === 'html' || lang === 'xml') {
            line = line.replace(/(&lt;\/?)([\w-]+)/g, '$1<span class="syn-tag">$2</span>');
            line = line.replace(/([\w-]+)(=)/g, '<span class="syn-attr">$1</span>$2');
        } else if (lang === 'css') {
            line = highlightKeywords(line, ['color','background','border','margin','padding','font','display','position','width','height','top','left','right','bottom','flex','grid','none','solid','auto','inherit','important']);
        } else if (lang === 'json') {
            line = line.replace(/(&quot;)([\w-]+)(&quot;)\s*:/g, '<span class="syn-attr">$1$2$3</span>:');
        } else if (lang === 'yaml') {
            line = line.replace(/^(\s*)([\w.-]+)(\s*:)/g, '$1<span class="syn-attr">$2</span>$3');
        } else if (lang === 'rust') {
            line = highlightKeywords(line, ['fn','let','mut','const','return','if','else','for','while','loop','match','struct','enum','impl','trait','pub','use','mod','crate','self','super','as','in','ref','move','async','await','where','type','true','false','Some','None','Ok','Err']);
        } else if (lang === 'sql') {
            line = highlightKeywords(line, ['SELECT','FROM','WHERE','INSERT','INTO','VALUES','UPDATE','SET','DELETE','CREATE','TABLE','DROP','ALTER','INDEX','JOIN','LEFT','RIGHT','INNER','OUTER','ON','AND','OR','NOT','NULL','AS','ORDER','BY','GROUP','HAVING','LIMIT','OFFSET','DISTINCT','UNION','ALL','EXISTS','IN','BETWEEN','LIKE','IS','COUNT','SUM','AVG','MAX','MIN']);
        }
        return line;
    }

    function highlightKeywords(line, keywords) {
        for (var i = 0; i < keywords.length; i++) {
            var kw = keywords[i];
            var regex = new RegExp('\\b(' + kw + ')\\b', 'g');
            line = line.replace(regex, function(match, p1, offset, str) {
                var before = str.substring(0, offset);
                var openSpans = (before.match(/<span/g) || []).length;
                var closeSpans = (before.match(/<\/span>/g) || []).length;
                if (openSpans > closeSpans) return match;
                return '<span class="syn-keyword">' + p1 + '</span>';
            });
        }
        return line;
    }

    function getLangFromExt(ext) {
        var map = {
            'js': 'js', 'jsx': 'js', 'mjs': 'js', 'ts': 'ts', 'tsx': 'ts',
            'go': 'go', 'py': 'py', 'sh': 'sh', 'bash': 'sh', 'zsh': 'sh',
            'java': 'java', 'c': 'c', 'cpp': 'c', 'h': 'c', 'hpp': 'c',
            'rs': 'rust', 'html': 'html', 'htm': 'html', 'xml': 'xml',
            'css': 'css', 'scss': 'css', 'less': 'css',
            'json': 'json', 'yaml': 'yaml', 'yml': 'yaml',
            'sql': 'sql', 'toml': 'yaml', 'ini': 'conf', 'cfg': 'conf', 'conf': 'conf',
            'env': 'sh', 'dockerfile': 'sh', 'makefile': 'sh',
            'rb': 'py', 'php': 'js', 'lua': 'py', 'r': 'py', 'swift': 'java', 'kt': 'java', 'scala': 'java',
            'tf': 'conf', 'hcl': 'conf', 'properties': 'conf',
            'md': 'none', 'txt': 'none', 'log': 'none', 'csv': 'none'
        };
        return map[ext] || 'none';
    }

    // =============================================
    //  EDITOR: SEARCH
    // =============================================
    window.toggleSearch = function() {
        viewerSearchOpen = !viewerSearchOpen;
        fvSearch.classList.toggle('open', viewerSearchOpen);
        if (viewerSearchOpen) {
            fvSearchInput.focus();
            fvSearchInput.select();
            if (fvSearchInput.value) searchInFile();
        } else {
            clearSearchHighlights();
        }
    };

    fvSearchInput.addEventListener('input', function() { searchInFile(); });
    fvSearchInput.addEventListener('keydown', function(e) {
        if (e.key === 'Enter') {
            e.preventDefault();
            if (e.shiftKey) searchPrev(); else searchNext();
        }
        if (e.key === 'Escape') { e.preventDefault(); toggleSearch(); }
    });

    function searchInFile() {
        if (viewerEditMode) return; // No search in edit mode
        clearSearchHighlights();
        var query = fvSearchInput.value;
        if (!query || !viewerRawContent) {
            fvSearchCount.textContent = '';
            viewerSearchMatches = [];
            viewerSearchCurrent = -1;
            return;
        }

        var lines = viewerRawContent.split('\n');
        var lineCount = lines.length;
        if (lines[lineCount - 1] === '' && lineCount > 1) lineCount--;
        var ext = getFileExt(viewedFileName || '');
        var queryLower = query.toLowerCase();
        viewerSearchMatches = [];

        var codeHtml = '';
        for (var j = 0; j < lineCount; j++) {
            var rawLine = lines[j];
            var lineHtml = escapeHtml(rawLine);
            var lineLower = rawLine.toLowerCase();
            var pos = 0;
            var matchParts = [];
            while (true) {
                var idx = lineLower.indexOf(queryLower, pos);
                if (idx === -1) break;
                viewerSearchMatches.push({ lineIdx: j, start: idx, end: idx + query.length });
                matchParts.push({ start: idx, end: idx + query.length });
                pos = idx + 1;
            }
            if (matchParts.length > 0) {
                lineHtml = highlightMatchesInLine(rawLine, matchParts, viewerSearchMatches.length - matchParts.length);
            } else {
                lineHtml = applySyntaxHighlight(lineHtml, ext);
            }
            codeHtml += '<span class="fv-line" data-line="' + j + '">' + (lineHtml || ' ') + '</span>\n';
        }
        fvCode.innerHTML = codeHtml;

        if (viewerSearchMatches.length > 0) {
            viewerSearchCurrent = 0;
            fvSearchCount.textContent = '1/' + viewerSearchMatches.length;
            scrollToMatch(0);
        } else {
            fvSearchCount.textContent = 'No results';
            viewerSearchCurrent = -1;
        }
    }

    function highlightMatchesInLine(rawLine, matches, globalOffset) {
        var result = '';
        var lastEnd = 0;
        for (var i = 0; i < matches.length; i++) {
            result += escapeHtml(rawLine.substring(lastEnd, matches[i].start));
            var matchText = escapeHtml(rawLine.substring(matches[i].start, matches[i].end));
            var matchId = globalOffset + i;
            result += '<span class="fv-match" data-match="' + matchId + '">' + matchText + '</span>';
            lastEnd = matches[i].end;
        }
        result += escapeHtml(rawLine.substring(lastEnd));
        return result;
    }

    function clearSearchHighlights() {
        var matches = fvCode.querySelectorAll('.fv-match');
        matches.forEach(function(el) { el.classList.remove('fv-match-current'); });
    }

    function scrollToMatch(idx) {
        if (idx < 0 || idx >= viewerSearchMatches.length) return;
        var prev = fvCode.querySelector('.fv-match-current');
        if (prev) prev.classList.remove('fv-match-current');
        var el = fvCode.querySelector('.fv-match[data-match="' + idx + '"]');
        if (el) {
            el.classList.add('fv-match-current');
            el.scrollIntoView({ behavior: 'smooth', block: 'center' });
            fvGutter.scrollTop = fvCode.scrollTop;
        }
        fvSearchCount.textContent = (idx + 1) + '/' + viewerSearchMatches.length;
    }

    window.searchNext = function() {
        if (viewerSearchMatches.length === 0) return;
        viewerSearchCurrent = (viewerSearchCurrent + 1) % viewerSearchMatches.length;
        scrollToMatch(viewerSearchCurrent);
    };
    window.searchPrev = function() {
        if (viewerSearchMatches.length === 0) return;
        viewerSearchCurrent = (viewerSearchCurrent - 1 + viewerSearchMatches.length) % viewerSearchMatches.length;
        scrollToMatch(viewerSearchCurrent);
    };

    // =============================================
    //  EDITOR: WORD WRAP & COPY
    // =============================================
    window.toggleWordWrap = function() {
        viewerWordWrap = !viewerWordWrap;
        fvCode.classList.toggle('wrap', viewerWordWrap);
        document.getElementById('fv-wrap-btn').classList.toggle('active', viewerWordWrap);
    };

    window.copyFileContent = function() {
        if (!viewerRawContent) return;
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(viewerRawContent).then(function() {
                fvStatusLeft.textContent = 'Copied to clipboard!';
                setTimeout(function() {
                    var lines = viewerRawContent.split('\n');
                    fvStatusLeft.textContent = 'Ln ' + lines.length + ', Col 1';
                }, 2000);
            }).catch(function() {});
        }
    };

    // =============================================
    //  VIEWER: OPEN / CLOSE
    // =============================================
    window.viewSelected = function() {
        if (selectedFiles.length === 1 && !selectedFiles[0].isDir && isTextFile(selectedFiles[0].name)) {
            viewFile(selectedFiles[0].fullPath, selectedFiles[0].name);
        }
    };

    window.closeFileViewer = function() {
        if (viewerModified && viewerEditMode) {
            if (!confirm('You have unsaved changes. Close anyway?')) return;
        }
        fileViewer.classList.remove('open');
        fvCode.innerHTML = '';
        fvCode.contentEditable = 'false';
        fvCode.classList.remove('fv-editable');
        fvCode.removeEventListener('input', onEditInput);
        fvGutter.innerHTML = '';
        fvFilename.textContent = '';
        fvInfoEl.textContent = '';
        viewedFilePath = null;
        viewedFileName = null;
        viewerRawContent = '';
        viewerSearchMatches = [];
        viewerSearchCurrent = -1;
        viewerEditMode = false;
        viewerModified = false;
        fvSearch.classList.remove('open');
        viewerSearchOpen = false;
        fvSearchInput.value = '';
    };

    window.downloadViewedFile = function() {
        if (viewedFilePath && viewedFileName) downloadFile(viewedFilePath, viewedFileName);
    };

    // =============================================
    //  DOWNLOAD (via SFTP HTTP API)
    // =============================================
    function downloadFile(path, filename) {
        var progressId = addProgress(filename, 'download');
        var url = sftpUrl('download', 'path=' + encodeURIComponent(path));

        fetch(url)
            .then(function(res) {
                if (!res.ok) throw new Error('Download failed: ' + res.status);
                return res.blob();
            })
            .then(function(blob) {
                completeProgress(progressId);
                var blobUrl = URL.createObjectURL(blob);
                var a = document.createElement('a');
                a.href = blobUrl;
                a.download = filename;
                document.body.appendChild(a);
                a.click();
                document.body.removeChild(a);
                setTimeout(function() { URL.revokeObjectURL(blobUrl); }, 1000);
            })
            .catch(function(err) {
                errorProgress(progressId, err.message);
            });
    }

    window.downloadSelected = function() {
        selectedFiles.forEach(function(f) {
            if (!f.isDir) downloadFile(f.fullPath, f.name);
        });
    };

    // =============================================
    //  UPLOAD (via SFTP HTTP API)
    // =============================================
    function uploadFiles(parentPath, fileList, useSudo) {
        if (!fileList || fileList.length === 0) return;
        var formData = new FormData();
        for (var i = 0; i < fileList.length; i++) {
            var f = fileList[i];
            var name = f.webkitRelativePath || f.name;
            formData.append('files', f, name);
        }

        var label = (useSudo ? '[sudo] ' : '') + fileList.length + ' file(s)';
        var progressId = addProgress(label, 'upload');

        var url = sftpUrl('upload', 'path=' + encodeURIComponent(parentPath));
        if (useSudo) url += '&sudo=true';

        fetch(url, {
            method: 'POST',
            body: formData
        })
        .then(function(res) { return res.json(); })
        .then(function(data) {
            if (data.error) {
                errorProgress(progressId, data.error);
                return;
            }
            var results = data.results || [];
            var errors = results.filter(function(r) { return r.error; });

            // Permission error: propose sudo retry
            if (data.permissionError && !useSudo && errors.length > 0) {
                errorProgress(progressId, 'Permission denied');
                if (confirm('Permission denied on upload.\n\nRetry upload with sudo?')) {
                    uploadFiles(parentPath, fileList, true);
                }
                return;
            }

            if (errors.length > 0) {
                errorProgress(progressId, errors.length + ' failed');
            } else {
                completeProgress(progressId);
            }
            if (currentPath === parentPath) listDirectory(parentPath);
        })
        .catch(function(err) {
            errorProgress(progressId, err.message);
        });
    }

    window.triggerUpload = function(isDir) {
        if (isDir) fmDirInput.click(); else fmFileInput.click();
    };

    fmFileInput.addEventListener('change', function() {
        uploadFiles(currentPath, fmFileInput.files);
        fmFileInput.value = '';
    });
    fmDirInput.addEventListener('change', function() {
        uploadFiles(currentPath, fmDirInput.files);
        fmDirInput.value = '';
    });

    // =============================================
    //  DRAG & DROP
    // =============================================
    var dragCounter = 0;
    fmPanel.addEventListener('dragenter', function(e) {
        e.preventDefault(); dragCounter++;
        fmDropOverlay.classList.add('visible');
    });
    fmPanel.addEventListener('dragleave', function(e) {
        e.preventDefault(); dragCounter--;
        if (dragCounter <= 0) { dragCounter = 0; fmDropOverlay.classList.remove('visible'); }
    });
    fmPanel.addEventListener('dragover', function(e) { e.preventDefault(); });
    fmPanel.addEventListener('drop', function(e) {
        e.preventDefault(); dragCounter = 0;
        fmDropOverlay.classList.remove('visible');
        if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
            uploadFiles(currentPath, e.dataTransfer.files);
        }
    });

    // =============================================
    //  PROGRESS / TRANSFERS
    // =============================================
    var progressCounter = 0;

    function addProgress(filename, type) {
        var id = 'progress-' + (++progressCounter);
        var div = document.createElement('div');
        div.className = 'fm-progress-item';
        div.id = id;
        var icon = document.createElement('span');
        icon.className = 'fm-icon';
        icon.textContent = type === 'upload' ? '\u2B06' : '\u2B07';
        icon.style.fontSize = '12px';
        var name = document.createElement('span');
        name.className = 'fm-progress-name';
        name.textContent = filename;
        name.title = filename;
        var bar = document.createElement('div');
        bar.className = 'fm-progress-bar';
        var fill = document.createElement('div');
        fill.className = 'fm-progress-fill';
        fill.style.width = '30%';
        bar.appendChild(fill);
        var status = document.createElement('span');
        status.className = 'fm-progress-status';
        status.textContent = 'In progress...';
        div.appendChild(icon); div.appendChild(name); div.appendChild(bar); div.appendChild(status);
        fmProgress.appendChild(div);
        fmProgress.scrollTop = fmProgress.scrollHeight;
        return id;
    }

    function completeProgress(id) {
        var div = document.getElementById(id);
        if (!div) return;
        div.classList.add('done');
        div.querySelector('.fm-progress-fill').style.width = '100%';
        div.querySelector('.fm-progress-status').textContent = 'Done';
        setTimeout(function() { if (div.parentNode) div.parentNode.removeChild(div); }, 5000);
    }
    function errorProgress(id, msg) {
        var div = document.getElementById(id);
        if (!div) return;
        div.classList.add('error');
        div.querySelector('.fm-progress-fill').style.width = '100%';
        div.querySelector('.fm-progress-status').textContent = msg || 'Error';
        setTimeout(function() { if (div.parentNode) div.parentNode.removeChild(div); }, 8000);
    }

    window.clearTransfers = function() { fmProgress.innerHTML = ''; };

    function formatBytes(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
        if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB';
        return (bytes / 1073741824).toFixed(1) + ' GB';
    }

    // =============================================
    //  UTILITY
    // =============================================
    window.refreshDirectory = function() { listDirectory(currentPath); };

    window.copyPath = function() {
        if (selectedFiles.length > 0) {
            copyToClipboard(selectedFiles.map(function(f) { return f.fullPath; }).join('\n'));
        } else {
            copyToClipboard(currentPath);
        }
    };

    function copyToClipboard(text) {
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(text).catch(function() {});
        }
    }

    // =============================================
    //  KEYBOARD SHORTCUTS
    // =============================================
    document.addEventListener('keydown', function(e) {
        var viewerOpen = fileViewer.classList.contains('open');

        if (e.key === 'Escape') {
            if (viewerOpen && viewerSearchOpen) {
                toggleSearch(); e.preventDefault(); e.stopPropagation(); return;
            }
            if (viewerOpen) {
                window.closeFileViewer(); e.preventDefault(); e.stopPropagation(); return;
            }
            if (fmCtx.classList.contains('visible')) {
                hideContextMenu(); e.preventDefault(); return;
            }
            if (selectedFiles.length > 0 && fmOpen) {
                window.clearSelection(); e.preventDefault(); return;
            }
        }

        if (viewerOpen) {
            // Ctrl+F = search
            if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
                e.preventDefault();
                if (!viewerSearchOpen) toggleSearch(); else fvSearchInput.focus();
                return;
            }
            // Ctrl+S = save (in edit mode)
            if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault();
                if (viewerEditMode) saveFile();
                return;
            }
            // Alt+Z = word wrap
            if (e.altKey && e.key === 'z') {
                e.preventDefault(); toggleWordWrap(); return;
            }
            return;
        }

        if (!fmOpen) return;

        if (e.key === 'F5') { e.preventDefault(); refreshDirectory(); return; }
        if (e.key === 'F2') { e.preventDefault(); renameSelected(); return; }
        if (e.key === 'Delete' && selectedFiles.length > 0) { e.preventDefault(); deleteSelected(); return; }
        if ((e.ctrlKey || e.metaKey) && e.key === 'a') { e.preventDefault(); selectAll(); return; }
        if ((e.ctrlKey || e.metaKey) && e.key === 'd') { e.preventDefault(); downloadSelected(); return; }
        if ((e.ctrlKey || e.metaKey) && e.key === 'u') { e.preventDefault(); triggerUpload(false); return; }
        if (e.key === 'Enter' && selectedFiles.length === 1) {
            e.preventDefault();
            var f = selectedFiles[0];
            if (f.isDir) listDirectory(f.fullPath);
            else if (isTextFile(f.name)) viewFile(f.fullPath, f.name);
            return;
        }
        if (e.key === 'Backspace' && currentPath !== '/') {
            e.preventDefault();
            listDirectory(currentPath.substring(0, currentPath.lastIndexOf('/')) || '/');
        }
    });

    // =============================================
    //  GUACAMOLE CONNECTION
    // =============================================
    guac.connect(connectParams);

    var keyboard = new Guacamole.Keyboard(document);

    // Track Ctrl / Cmd(Meta) / Shift separately.
    var ctrlPressed = false, metaPressed = false, shiftPressed = false;

    keyboard.onkeydown = function(keysym) {
        // If a real form field is focused (a modal prompt, the file-manager search,
        // the file viewer/editor…), let the browser handle the key — do NOT forward
        // it to the remote terminal. The hidden clipboard-helper textarea is not a
        // "real" field, so normal terminal typing still flows through.
        var ae = document.activeElement;
        if (ae && ae.id !== 'clipboard-helper' &&
            (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA' || ae.isContentEditable)) {
            return true;
        }

        if (keysym === 0xFFE3 || keysym === 0xFFE4) { ctrlPressed = true; guac.sendKeyEvent(1, keysym); return true; }
        if (keysym === 0xFFE7 || keysym === 0xFFE8) { metaPressed = true; guac.sendKeyEvent(1, keysym); return true; }
        if (keysym === 0xFFE1 || keysym === 0xFFE2) { shiftPressed = true; guac.sendKeyEvent(1, keysym); return true; }

        // Clipboard/selection combos are handled by the browser (via the hidden
        // textarea), NOT forwarded to the remote:
        //   • Cmd+C / Cmd+V / Cmd+X / Cmd+A  (macOS)
        //   • Ctrl+Shift+C / …             (Linux/Windows terminal convention)
        // Plain Ctrl+C stays a terminal key, so it still sends SIGINT (interrupt).
        //
        // Guacamole.Keyboard convention (verified in guacamole-common.min.js):
        // onkeydown must return TRUE to *allow the event through* to the browser
        // (Guacamole does NOT preventDefault), and FALSE to *block* it (Guacamole
        // DOES preventDefault, via `defaultPrevented = !press()`). Returning false
        // here would preventDefault the Cmd/Ctrl+V keydown and SUPPRESS the native
        // `paste` event — which is exactly why paste used to fail. Return true so
        // the browser fires the real copy/paste on the focused hidden textarea.
        var clipKey = (keysym === 0x63 || keysym === 0x43 ||  // c C
                       keysym === 0x76 || keysym === 0x56 ||  // v V
                       keysym === 0x61 || keysym === 0x41 ||  // a A
                       keysym === 0x78 || keysym === 0x58);   // x X
        if (clipKey && (metaPressed || (ctrlPressed && shiftPressed))) {
            return true;   // let the browser perform the clipboard action (do NOT send to remote)
        }

        guac.sendKeyEvent(1, keysym);
        return true;
    };

    keyboard.onkeyup = function(keysym) {
        if (keysym === 0xFFE3 || keysym === 0xFFE4) ctrlPressed = false;
        if (keysym === 0xFFE7 || keysym === 0xFFE8) metaPressed = false;
        if (keysym === 0xFFE1 || keysym === 0xFFE2) shiftPressed = false;
        guac.sendKeyEvent(0, keysym);
    };

    // STUCK-KEY GUARD. If a browser shortcut steals focus mid-keystroke (e.g.
    // Firefox opens quick-find on "/"), the matching key-up is delivered to the
    // browser chrome instead of Guacamole, so the key stays "pressed" and repeats
    // forever (the runaway "////" bug). Releasing every key whenever the terminal
    // loses focus or is hidden clears any such stuck key.
    function releaseAllKeys() {
        try { keyboard.reset(); } catch (e) {}
        ctrlPressed = false; metaPressed = false; shiftPressed = false;
    }
    window.addEventListener('blur', releaseAllKeys);
    document.addEventListener('visibilitychange', function() {
        if (document.hidden) releaseAllKeys();
    });

    // Keep terminal keystrokes in the terminal: stop the browser from hijacking
    // keys that would otherwise trigger a shortcut and steal focus (Firefox's "/"
    // and "'" quick-find, backspace navigation, etc.). Skip real input fields
    // (file-manager / viewer search) and clipboard shortcuts.
    document.addEventListener('keydown', function(e) {
        var t = e.target;
        if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
        if (e.ctrlKey || e.metaKey || e.altKey) return; // let clipboard/browser combos through
        if (e.key === '/' || e.key === "'" || e.key === 'Backspace') {
            e.preventDefault();
        }
    }, true);

    var mouse = new Guacamole.Mouse(guacDisplay.getElement());
    mouse.onmousedown = mouse.onmouseup = mouse.onmousemove = function(ms) { guac.sendMouseState(ms); };

    var touch = new Guacamole.Mouse.Touchpad(guacDisplay.getElement());
    touch.onmousedown = touch.onmouseup = touch.onmousemove = function(ms) { guac.sendMouseState(ms); };

    window.addEventListener('resize', resizeGuac);

    // PASTE — cross-browser (Chrome + Firefox). The reliable, permission-free way
    // to read the clipboard is the browser's native "paste" event (fired by both
    // Ctrl/Cmd+V and right-click > Paste) on the focused hidden textarea. For that
    // event to fire, the Ctrl/Cmd+V keydown must NOT be preventDefault-ed — and
    // Guacamole.Keyboard preventDefaults whenever onkeydown returns false. That is
    // why the keydown handler above returns TRUE for clipboard combos (see the note
    // there): true = "allow through to the browser" in Guacamole's inverted convention,
    // so the browser fires the real paste here.
    var clip = document.getElementById('clipboard-helper');
    function handlePaste(e) {
        var cd = e.clipboardData || window.clipboardData;
        if (!cd) return;
        var text = cd.getData('text/plain') || cd.getData('text') || '';
        if (text) {
            // Stop here so the same event doesn't ALSO reach the document-level
            // fallback listener below (paste on the textarea bubbles to document),
            // which would send the text twice.
            e.preventDefault();
            e.stopPropagation();
            sendTextToTerminal(text);
        }
        if (clip) clip.value = '';
    }
    if (clip) {
        clip.addEventListener('paste', handlePaste);
        // Guacamole (its document listener) sends the actual keystroke to the
        // remote. We block every key except clipboard combos so nothing piles up
        // in the hidden field, while Cmd/Ctrl+C/V/X/A still reach the browser as
        // real clipboard actions (and "/" no longer triggers browser quick-find,
        // because focus is on an editable element).
        clip.addEventListener('keydown', function(e) {
            var k = (e.key || '').toLowerCase();
            var combo = (e.ctrlKey || e.metaKey) && (k === 'c' || k === 'v' || k === 'x' || k === 'a');
            if (!combo) e.preventDefault();
        });
        clip.addEventListener('input', function() { clip.value = ''; });

        // Keep the capture area focused, but never steal focus from real inputs
        // (file-manager search, viewer, modal).
        function focusClip() {
            var a = document.activeElement;
            if (a && a !== clip && (a.tagName === 'INPUT' || a.tagName === 'TEXTAREA' || a.isContentEditable)) return;
            try { clip.focus({ preventScroll: true }); } catch (e2) { try { clip.focus(); } catch (e3) {} }
        }
        window.focusClip = focusClip;
        document.addEventListener('mouseup', function() { setTimeout(focusClip, 0); });
        document.addEventListener('click', function() { setTimeout(focusClip, 0); });
        window.addEventListener('focus', focusClip);
        setTimeout(focusClip, 400);
    }
    // Fallback: some browsers do fire paste on the document itself.
    document.addEventListener('paste', handlePaste);

    // ---- Terminal right-click menu (Copy / Paste) + reliable paste modal ----
    var lastSelection = '';   // updated by guac.onclipboard on terminal selection
    var termCtx = document.getElementById('term-ctx');
    var displayEl = document.getElementById('display');

    function doCopy() {
        var text = lastSelection || (clip && clip.value) || '';
        if (text) copyToClipboard(text);
        else showToast('Select text in the terminal first');
    }
    function doPaste() {
        // Try the async clipboard (Chrome, with permission); otherwise open a modal
        // where the user pastes manually — reliable on every browser incl. Firefox.
        if (navigator.clipboard && navigator.clipboard.readText) {
            navigator.clipboard.readText().then(function(t) {
                if (t) sendTextToTerminal(t); else openPasteModal();
            }).catch(function() { openPasteModal(); });
        } else {
            openPasteModal();
        }
    }
    function openPasteModal() {
        var ov = document.getElementById('paste-modal');
        var ta = document.getElementById('paste-modal-input');
        var send = document.getElementById('paste-modal-send');
        var cancel = document.getElementById('paste-modal-cancel');
        ta.value = '';
        ov.classList.add('open');
        setTimeout(function() { ta.focus(); }, 30);
        function close() { ov.classList.remove('open'); send.onclick = cancel.onclick = ov.onmousedown = ta.onkeydown = null; if (window.focusClip) window.focusClip(); }
        send.onclick = function() { var t = ta.value; close(); if (t) sendTextToTerminal(t); };
        cancel.onclick = close;
        ov.onmousedown = function(e) { if (e.target === ov) close(); };
        ta.onkeydown = function(e) { e.stopPropagation(); if (e.key === 'Escape') { e.preventDefault(); close(); } };
    }
    if (termCtx && displayEl) {
        displayEl.addEventListener('contextmenu', function(e) {
            e.preventDefault();
            termCtx.style.left = Math.min(e.clientX, window.innerWidth - 190) + 'px';
            termCtx.style.top = Math.min(e.clientY, window.innerHeight - 90) + 'px';
            termCtx.classList.add('visible');
        });
        document.addEventListener('click', function() { termCtx.classList.remove('visible'); });
        termCtx.addEventListener('click', function(e) {
            var it = e.target.closest('.fm-ctx-item'); if (!it) return;
            termCtx.classList.remove('visible');
            var act = it.getAttribute('data-tact');
            if (act === 'copy') doCopy();
            else if (act === 'paste') doPaste();
        });
    }
    window.termCopy = doCopy;
    window.termPaste = doPaste;

    // Helper: send text char-by-char to remote terminal
    // Modifier keysyms (Shift/Ctrl/Meta/Alt, left & right).
    var MODIFIER_KEYSYMS = [0xFFE1, 0xFFE2, 0xFFE3, 0xFFE4, 0xFFE7, 0xFFE8, 0xFFE9, 0xFFEA];

    function sendTextToTerminal(text) {
        // A paste shortcut (Cmd+V on macOS, Ctrl+Shift+V on Linux/Windows) keeps
        // its modifier key physically held while this runs, and we already sent
        // that modifier "down" to the remote. If we injected the pasted text now,
        // every character would arrive as Meta+char / Ctrl+char and be eaten by the
        // shell's key bindings — e.g. readline treats Meta+c as "capitalize-word",
        // so the "c" never appears. Release every modifier on the remote first so
        // the pasted text lands as plain, literal characters.
        for (var m = 0; m < MODIFIER_KEYSYMS.length; m++) {
            guac.sendKeyEvent(0, MODIFIER_KEYSYMS[m]);
        }
        ctrlPressed = false; metaPressed = false; shiftPressed = false;

        for (var i = 0; i < text.length; i++) {
            var charCode = text.charCodeAt(i);
            var keysym;
            if (charCode === 10 || charCode === 13) keysym = 0xFF0D;
            else if (charCode === 9) keysym = 0xFF09;
            else if (charCode === 8) keysym = 0xFF08;
            else if (charCode < 0x100) keysym = charCode;
            else keysym = 0x01000000 | charCode;
            guac.sendKeyEvent(1, keysym);
            guac.sendKeyEvent(0, keysym);
        }
    }

    // ARGV: guacd streams the current connection argument values (color-scheme,
    // font-name, font-size, …). Without an onargv handler, guacamole-common-js
    // NAKs each stream with "Receiving argument values unsupported" (status 256) —
    // a protocol error the native senhasegura client does not produce. Accept and
    // drain the streams so the handshake matches native behavior. We don't need
    // the values, but consuming the stream avoids the rejection.
    guac.onargv = function(stream, mimetype, name) {
        var reader = new Guacamole.StringReader(stream);
        reader.ontext = function() { /* consume */ };
        reader.onend = function() { /* stream fully read & acked */ };
    };

    // COPY: remote clipboard (a terminal selection) -> browser clipboard.
    // Try the async Clipboard API, then fall back to a hidden-textarea +
    // execCommand('copy') which works where writeText is blocked.
    function copyToClipboard(text) {
        if (!text) return;
        var done = function(ok) { showToast(ok ? 'Copied to clipboard' : 'Copy blocked by the browser'); };
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(text).then(function() { done(true); }, function() { done(legacyCopy(text)); });
        } else {
            done(legacyCopy(text));
        }
    }
    function legacyCopy(text) {
        try {
            var ta = document.createElement('textarea');
            ta.value = text;
            ta.setAttribute('readonly', '');
            ta.style.cssText = 'position:fixed;top:0;left:-9999px;opacity:0;';
            document.body.appendChild(ta);
            ta.select(); ta.setSelectionRange(0, text.length);
            var ok = document.execCommand('copy');
            document.body.removeChild(ta);
            return ok;
        } catch (e) { return false; }
    }
    var toastTimer;
    function showToast(msg) {
        var el = document.getElementById('segura-toast');
        if (!el) {
            el = document.createElement('div');
            el.id = 'segura-toast';
            el.style.cssText = 'position:fixed;bottom:20px;left:50%;transform:translateX(-50%);' +
                'background:#0d3330;color:#e7f0ef;border:1px solid #1f4f4a;border-left:3px solid #7ee787;' +
                'padding:9px 16px;border-radius:8px;font:600 12.5px/1 "Inter",Helvetica,Arial,sans-serif;' +
                'z-index:500;box-shadow:0 8px 24px rgba(0,0,0,.35);opacity:0;transition:opacity .18s;pointer-events:none;';
            document.body.appendChild(el);
        }
        el.textContent = msg;
        el.style.opacity = '1';
        clearTimeout(toastTimer);
        toastTimer = setTimeout(function() { el.style.opacity = '0'; }, 1600);
    }

    guac.onclipboard = function(stream, mimetype) {
        if (mimetype === 'text/plain') {
            var reader = new Guacamole.StringReader(stream);
            var data = '';
            reader.ontext = function(text) { data += text; };
            reader.onend = function() {
                // A terminal selection just landed. Remember it (for the right-click
                // Copy), copy it to the system clipboard now (copy-on-select), and
                // stage it in the hidden field so Cmd/Ctrl+C copies the same text.
                lastSelection = data;
                copyToClipboard(data);
                if (clip) { clip.value = data; try { clip.select(); } catch (e) {} }
            };
        }
    };

    window.disconnect = function() {
        guac.disconnect();
        setTimeout(function() { window.location.href = '/'; }, 500);
    };

    window.addEventListener('beforeunload', function() { guac.disconnect(); });
})();
