// Drives the publish and metadata forms on /upload.
//
// XMLHttpRequest rather than fetch for the archive upload, because only XHR
// reports upload progress and a release archive is easily tens of megabytes.
(function () {
  var appInput = document.getElementById('f-app');
  var tokenInput = document.getElementById('f-token');
  var appHint = document.getElementById('app-hint');
  if (!appInput) return;

  // ------------------------------------------------------------- utilities

  function appID() { return appInput.value.trim(); }
  function token() { return tokenInput.value.trim(); }

  function show(node, text, ok) {
    node.hidden = false;
    node.textContent = text;
    node.classList.toggle('ok', ok === true);
    node.classList.toggle('fail', ok === false);
  }

  function mb(bytes) { return (bytes / 1048576).toFixed(1) + ' MB'; }

  function authHeaders(extra) {
    var headers = { Authorization: 'Bearer ' + token() };
    if (extra) Object.keys(extra).forEach(function (k) { headers[k] = extra[k]; });
    return headers;
  }

  // Complain early rather than letting the server return a 401.
  function requireCredentials(result) {
    if (!appID()) { result('请先在上面填写应用 ID。'); return false; }
    if (!token()) { result('请先在上面填写上传令牌。'); return false; }
    return true;
  }

  function csv(value) {
    return value
      .split(/[,，]/)
      .map(function (s) { return s.trim(); })
      .filter(function (s) { return s.length > 0; });
  }

  // --------------------------------------------------- application picker

  // Populate the suggestion list so the id can be picked instead of typed. The
  // field stays free text: publishing creates an application that does not
  // exist yet.
  fetch('/api/v1/apps')
    .then(function (r) { return r.json(); })
    .then(function (data) {
      var list = document.getElementById('app-ids');
      (data.apps || []).forEach(function (app) {
        var option = document.createElement('option');
        option.value = app.id;
        if (app.name && app.name !== app.id) option.label = app.name;
        list.appendChild(option);
      });
    })
    .catch(function () { /* suggestions are a convenience, not a requirement */ });

  // ============================================================ publish form

  var uploadForm = document.getElementById('upload-form');
  var fileInput = document.getElementById('f-file');
  var dropZone = document.getElementById('drop');
  var dropFile = document.getElementById('drop-file');
  var uploadButton = document.getElementById('submit');
  var uploadResult = document.getElementById('upload-result');
  var barWrap = document.getElementById('progress-wrap');
  var bar = document.getElementById('progress-bar');
  var barText = document.getElementById('progress-text');

  function refreshFileName() {
    var file = fileInput.files && fileInput.files[0];
    dropFile.textContent = file ? file.name + ' · ' + mb(file.size) : '';
    dropZone.classList.toggle('has-file', !!file);
  }

  ['dragenter', 'dragover'].forEach(function (event) {
    dropZone.addEventListener(event, function (e) {
      e.preventDefault();
      dropZone.classList.add('over');
    });
  });
  ['dragleave', 'dragend'].forEach(function (event) {
    dropZone.addEventListener(event, function () { dropZone.classList.remove('over'); });
  });
  dropZone.addEventListener('drop', function (e) {
    e.preventDefault();
    dropZone.classList.remove('over');
    var files = e.dataTransfer && e.dataTransfer.files;
    if (files && files.length) {
      fileInput.files = files;
      refreshFileName();
    }
  });
  fileInput.addEventListener('change', refreshFileName);

  function setProgress(ratio, text) {
    barWrap.hidden = false;
    bar.style.width = Math.round(ratio * 100) + '%';
    barText.textContent = text;
  }

  uploadForm.addEventListener('submit', function (event) {
    event.preventDefault();
    if (!requireCredentials(function (m) { show(uploadResult, m, false); })) return;

    var version = document.getElementById('f-version').value.trim();
    var file = fileInput.files && fileInput.files[0];
    if (!file) { show(uploadResult, '请选择发布包。', false); return; }

    var body = new FormData();
    body.append('file', file);
    if (version) body.append('version', version);

    uploadButton.disabled = true;
    uploadResult.hidden = true;
    setProgress(0, '正在上传 ' + file.name + '（' + mb(file.size) + '）…');

    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1/apps/' + encodeURIComponent(appID()) + '/upload');
    xhr.setRequestHeader('Authorization', 'Bearer ' + token());

    xhr.upload.onprogress = function (e) {
      if (!e.lengthComputable) return;
      var ratio = e.loaded / e.total;
      if (ratio < 1) {
        setProgress(ratio, '正在上传 ' + mb(e.loaded) + ' / ' + mb(e.total) +
          '（' + Math.round(ratio * 100) + '%）');
      } else {
        setProgress(1, '上传完成，站点正在解包…');
      }
    };

    xhr.onload = function () {
      uploadButton.disabled = false;
      barWrap.hidden = true;
      var data = {};
      try { data = JSON.parse(xhr.responseText); } catch (e) { /* leave it empty */ }

      if (xhr.status === 201) {
        var lines = [
          '发布成功', '',
          '应用      ' + data.app,
          '版本      ' + data.version,
          '文件      ' + (data.files || []).length + ' 个，共 ' + mb(data.bytes || 0),
          '同名版本  ' + (data.replaced ? '已存在，已整体替换' : '不存在，新建'),
          '',
        ];
        (data.files || []).forEach(function (f) { lines.push('  ' + f.file); });
        if (data.pageUrl) lines.push('', data.pageUrl);
        show(uploadResult, lines.join('\n'), true);
        fileInput.value = '';
        refreshFileName();
      } else {
        show(uploadResult, '发布失败（' + xhr.status + '）\n\n' +
          ((data.error && data.error.message) || 'HTTP ' + xhr.status), false);
      }
    };
    xhr.onerror = function () {
      uploadButton.disabled = false;
      barWrap.hidden = true;
      show(uploadResult, '请求失败，网络中断或服务不可达。', false);
    };
    xhr.send(body);
  });

  // =========================================================== metadata form

  var metaForm = document.getElementById('meta-form');
  var metaResult = document.getElementById('meta-result');
  var metaButton = document.getElementById('meta-save');
  var iconInput = document.getElementById('m-icon');
  var iconName = document.getElementById('m-icon-name');
  var iconPreview = document.getElementById('m-icon-preview');

  // The form fields, and the app.json keys they map to.
  var FIELDS = {
    name: 'm-name', vendor: 'm-vendor', license: 'm-license',
    homepage: 'm-homepage', channel: 'm-channel', summary: 'm-summary',
    description: 'm-description', tags: 'm-tags', platforms: 'm-platforms',
    order: 'm-order', hidden: 'm-hidden',
  };

  // current holds the metadata as loaded, so a save can merge onto it and keep
  // fields the form does not show, the icon name above all.
  var current = null;

  function fillForm(meta) {
    meta = meta || {};
    document.getElementById('m-name').value = meta.name || '';
    document.getElementById('m-vendor').value = meta.vendor || '';
    document.getElementById('m-license').value = meta.license || '';
    document.getElementById('m-homepage').value = meta.homepage || '';
    document.getElementById('m-channel').value = meta.channel || '';
    document.getElementById('m-summary').value = meta.summary || '';
    document.getElementById('m-description').value = meta.description || '';
    document.getElementById('m-tags').value = (meta.tags || []).join(', ');
    document.getElementById('m-platforms').value = (meta.platforms || []).join(', ');
    document.getElementById('m-order').value =
      (meta.order === null || meta.order === undefined) ? '' : meta.order;
    document.getElementById('m-hidden').checked = !!meta.hidden;
  }

  function payloadFromForm() {
    var payload = Object.assign({}, current || {});
    var value = function (key) { return document.getElementById(FIELDS[key]).value.trim(); };

    payload.name = value('name');
    payload.vendor = value('vendor');
    payload.license = value('license');
    payload.homepage = value('homepage');
    payload.channel = value('channel');
    payload.summary = value('summary');
    payload.description = document.getElementById('m-description').value.trim();
    payload.tags = csv(value('tags'));
    payload.platforms = csv(value('platforms'));
    payload.hidden = document.getElementById('m-hidden').checked;

    var order = value('order');
    payload.order = order === '' ? null : Number(order);
    if (payload.order !== null && !isFinite(payload.order)) payload.order = null;

    // The icon file name is owned by the icon upload; never send a stale one.
    if (current && current.icon) payload.icon = current.icon;
    return payload;
  }

  function showIcon(url, file) {
    if (url) {
      iconPreview.src = url + (url.indexOf('?') < 0 ? '?' : '&') + 't=' + Date.now();
      iconPreview.hidden = false;
    } else {
      iconPreview.hidden = true;
    }
    iconName.textContent = file || '';
  }

  function loadMetadata(quiet) {
    if (!requireCredentials(function (m) { show(metaResult, m, false); })) return;
    fetch('/api/v1/apps/' + encodeURIComponent(appID()) + '/metadata', {
      headers: authHeaders(),
    })
      .then(function (r) {
        return r.json().then(function (data) { return { ok: r.ok, status: r.status, data: data }; });
      })
      .then(function (r) {
        if (!r.ok) {
          show(metaResult, '读取失败（' + r.status + '）\n\n' +
            ((r.data.error && r.data.error.message) || ''), false);
          return;
        }
        current = r.data.metadata || {};
        fillForm(current);
        showIcon(current.icon ? r.data.iconUrl : '', current.icon);
        if (!quiet) {
          show(metaResult, '已载入 ' + appID() + ' 的当前信息。', true);
        }
      })
      .catch(function (err) {
        show(metaResult, '读取失败：' + err.message, false);
      });
  }

  document.getElementById('meta-load').addEventListener('click', function () {
    loadMetadata(false);
  });

  // Load automatically when the application id changes, so the form is rarely
  // filled in by hand.
  var lastLoaded = '';
  appInput.addEventListener('change', function () {
    var id = appID();
    if (!id || id === lastLoaded || !token()) return;
    lastLoaded = id;
    loadMetadata(true);
  });

  iconInput.addEventListener('change', function () {
    var file = iconInput.files && iconInput.files[0];
    iconName.textContent = file ? file.name + ' · ' + mb(file.size) : '';
    if (file) {
      var reader = new FileReader();
      reader.onload = function () {
        iconPreview.src = reader.result;
        iconPreview.hidden = false;
      };
      reader.readAsDataURL(file);
    }
  });

  metaForm.addEventListener('submit', function (event) {
    event.preventDefault();
    if (!requireCredentials(function (m) { show(metaResult, m, false); })) return;

    metaButton.disabled = true;
    show(metaResult, '正在保存…');

    // Always read first so a save cannot drop fields the form does not show.
    fetch('/api/v1/apps/' + encodeURIComponent(appID()) + '/metadata', { headers: authHeaders() })
      .then(function (r) {
        if (r.ok) return r.json();
        return { metadata: {} };
      })
      .then(function (loaded) {
        current = Object.assign({}, loaded.metadata || {}, current || {});
        return fetch('/api/v1/apps/' + encodeURIComponent(appID()) + '/metadata', {
          method: 'PUT',
          headers: authHeaders({ 'Content-Type': 'application/json' }),
          body: JSON.stringify(payloadFromForm()),
        });
      })
      .then(function (r) {
        return r.json().then(function (data) { return { ok: r.ok, status: r.status, data: data }; });
      })
      .then(function (r) {
        if (!r.ok) {
          throw new Error((r.data.error && r.data.error.message) || ('HTTP ' + r.status));
        }
        current = r.data.metadata || {};
        showIcon(current.icon ? r.data.iconUrl : '', current.icon);
        return saveIconIfChosen();
      })
      .then(function (iconMessage) {
        metaButton.disabled = false;
        var lines = ['应用信息已保存', ''];
        if (iconMessage) lines.push(iconMessage, '');
        lines.push('站点正在重新扫描，一两秒后生效。');
        show(metaResult, lines.join('\n'), true);
      })
      .catch(function (err) {
        metaButton.disabled = false;
        show(metaResult, '保存失败\n\n' + err.message, false);
      });
  });

  // saveIconIfChosen uploads the picked icon, if any, and reports what it did.
  function saveIconIfChosen() {
    var file = iconInput.files && iconInput.files[0];
    if (!file) return Promise.resolve('');

    return fetch('/api/v1/apps/' + encodeURIComponent(appID()) + '/icon', {
      method: 'PUT',
      headers: authHeaders({ 'Content-Type': file.type || 'application/octet-stream' }),
      body: file,
    })
      .then(function (r) {
        return r.json().then(function (data) { return { ok: r.ok, status: r.status, data: data }; });
      })
      .then(function (r) {
        if (!r.ok) {
          throw new Error('图标上传失败：' +
            ((r.data.error && r.data.error.message) || ('HTTP ' + r.status)));
        }
        current = r.data.metadata || current;
        showIcon(r.data.iconUrl, current.icon);
        iconInput.value = '';
        return '图标已更新为 ' + current.icon;
      });
  }
})();
