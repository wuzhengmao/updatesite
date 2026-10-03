// Drive the upload form: post the archive to the publish endpoint and show the
// JSON answer in place.
(function () {
  var form = document.getElementById('upload-form');
  var out = document.getElementById('upload-result');
  if (!form || !out) return;

  function show(text, ok) {
    out.hidden = false;
    out.textContent = text;
    out.classList.toggle('ok', ok === true);
    out.classList.toggle('fail', ok === false);
  }

  form.addEventListener('submit', function (event) {
    event.preventDefault();

    var data = new FormData(form);
    var app = String(data.get('app') || '').trim();
    var token = String(data.get('token') || '').trim();
    var version = String(data.get('version') || '').trim();
    var file = data.get('file');

    if (!file || !file.size) {
      show('请先选择压缩包。', false);
      return;
    }

    var body = new FormData();
    body.append('file', file);
    if (version) body.append('version', version);

    var button = form.querySelector('button');
    button.disabled = true;
    show('正在上传 ' + file.name + '（' + (file.size / 1048576).toFixed(1) + ' MB）…');

    fetch('/api/v1/apps/' + encodeURIComponent(app) + '/upload', {
      method: 'POST',
      headers: { Authorization: 'Bearer ' + token },
      body: body,
    })
      .then(function (resp) {
        return resp.json().catch(function () { return {}; }).then(function (json) {
          return { ok: resp.ok, status: resp.status, json: json };
        });
      })
      .then(function (r) {
        if (r.ok) {
          var lines = [
            '发布成功',
            '',
            '应用      ' + r.json.app,
            '版本      ' + r.json.version,
            '文件      ' + (r.json.files || []).length + ' 个，共 ' +
              (r.json.bytes / 1048576).toFixed(2) + ' MB',
            '同名版本  ' + (r.json.replaced ? '已存在，已整体替换' : '不存在，新建'),
            '',
          ];
          (r.json.files || []).forEach(function (f) {
            lines.push('  ' + f.file);
          });
          lines.push('', r.json.pageUrl || '');
          show(lines.join('\n'), true);
          form.querySelector('input[type=file]').value = '';
        } else {
          var message = (r.json.error && r.json.error.message) || ('HTTP ' + r.status);
          show('发布失败（' + r.status + '）\n\n' + message, false);
        }
      })
      .catch(function (err) {
        show('请求失败：' + err.message, false);
      })
      .finally(function () {
        button.disabled = false;
      });
  });
})();
