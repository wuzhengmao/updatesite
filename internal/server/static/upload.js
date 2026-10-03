// Drive the upload form: post the archive to the publish endpoint and show the
// JSON answer in place.
//
// XMLHttpRequest rather than fetch, because only XHR reports upload progress
// and a release archive is easily tens of megabytes.
(function () {
  var form = document.getElementById('upload-form');
  if (!form) return;

  var appInput = document.getElementById('f-app');
  var tokenInput = document.getElementById('f-token');
  var versionInput = document.getElementById('f-version');
  var fileInput = document.getElementById('f-file');
  var dropZone = document.getElementById('drop');
  var dropFile = document.getElementById('drop-file');
  var button = document.getElementById('submit');
  var out = document.getElementById('upload-result');
  var barWrap = document.getElementById('progress-wrap');
  var bar = document.getElementById('progress-bar');
  var barText = document.getElementById('progress-text');

  // ------------------------------------------------------------- utilities

  function show(text, ok) {
    out.hidden = false;
    out.textContent = text;
    out.classList.toggle('ok', ok === true);
    out.classList.toggle('fail', ok === false);
  }

  function mb(bytes) {
    return (bytes / 1048576).toFixed(1) + ' MB';
  }

  function setProgress(ratio, text) {
    barWrap.hidden = false;
    var pct = Math.round(ratio * 100);
    bar.style.width = pct + '%';
    barText.textContent = text || pct + '%';
  }

  function hideProgress() {
    barWrap.hidden = true;
    bar.style.width = '0%';
  }

  // --------------------------------------------------- application picker

  // Populate the suggestion list so the id can be picked instead of typed. The
  // field stays free text: uploading creates an application that does not
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

  // -------------------------------------------------------------- drop zone

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
    dropZone.addEventListener(event, function () {
      dropZone.classList.remove('over');
    });
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

  // ---------------------------------------------------------------- submit

  form.addEventListener('submit', function (event) {
    event.preventDefault();

    var app = appInput.value.trim();
    var token = tokenInput.value.trim();
    var version = versionInput.value.trim();
    var file = fileInput.files && fileInput.files[0];

    // The form is novalidate so that a hidden file input cannot block
    // submission with a browser message; check the fields here instead.
    if (!app) { show('请填写应用 ID。', false); appInput.focus(); return; }
    if (!token) { show('请填写上传令牌。', false); tokenInput.focus(); return; }
    if (!file) { show('请选择发布包。', false); dropZone.focus(); return; }

    var body = new FormData();
    body.append('file', file);
    if (version) body.append('version', version);

    button.disabled = true;
    out.hidden = true;
    setProgress(0, '正在上传 ' + file.name + '（' + mb(file.size) + '）…');

    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1/apps/' + encodeURIComponent(app) + '/upload');
    xhr.setRequestHeader('Authorization', 'Bearer ' + token);

    xhr.upload.onprogress = function (e) {
      if (!e.lengthComputable) return;
      var ratio = e.loaded / e.total;
      if (ratio < 1) {
        setProgress(ratio, '正在上传 ' + mb(e.loaded) + ' / ' + mb(e.total) + '（' + Math.round(ratio * 100) + '%）');
      } else {
        setProgress(1, '上传完成，站点正在解包…');
      }
    };

    xhr.onload = function () {
      button.disabled = false;
      hideProgress();

      var data = {};
      try { data = JSON.parse(xhr.responseText); } catch (e) { /* keep it empty */ }

      if (xhr.status === 201) {
        var lines = [
          '发布成功',
          '',
          '应用      ' + data.app,
          '版本      ' + data.version,
          '文件      ' + (data.files || []).length + ' 个，共 ' + mb(data.bytes || 0),
          '同名版本  ' + (data.replaced ? '已存在，已整体替换' : '不存在，新建'),
          '',
        ];
        (data.files || []).forEach(function (f) { lines.push('  ' + f.file); });
        if (data.pageUrl) lines.push('', data.pageUrl);
        show(lines.join('\n'), true);
        fileInput.value = '';
        refreshFileName();
      } else {
        var message = (data.error && data.error.message) || ('HTTP ' + xhr.status);
        show('发布失败（' + xhr.status + '）\n\n' + message, false);
      }
    };

    xhr.onerror = function () {
      button.disabled = false;
      hideProgress();
      show('请求失败，网络中断或服务不可达。', false);
    };

    xhr.onabort = function () {
      button.disabled = false;
      hideProgress();
      show('上传已取消。', false);
    };

    xhr.send(body);
  });
})();
