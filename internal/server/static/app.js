// Copy a full SHA-256 to the clipboard when its short form is clicked.
document.addEventListener('click', function (event) {
  var el = event.target.closest('[data-copy]');
  if (!el) return;
  var value = el.getAttribute('data-copy');
  var done = function (ok) {
    var previous = el.textContent;
    el.textContent = ok ? '已复制' : '复制失败';
    setTimeout(function () { el.textContent = previous; }, 1200);
  };
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(value).then(function () { done(true); }, function () { done(false); });
    return;
  }
  var input = document.createElement('textarea');
  input.value = value;
  input.setAttribute('readonly', '');
  input.style.position = 'fixed';
  input.style.opacity = '0';
  document.body.appendChild(input);
  input.select();
  var ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
  document.body.removeChild(input);
  done(ok);
});
