namespace LanAi.Workspace.Injection;

/// <summary>
/// The 共飞 status bar injected into the official client.
/// </summary>
/// <remarks>
/// Renders whatever state <c>window.__cofly.render</c> is handed, so the C# side owns
/// all wording and policy. Kept deliberately inert: fixed position, no pointer events,
/// no interference with the app's own layout or input.
/// </remarks>
internal static class CoflyOverlayScript
{
    internal const string Source = """
        (function () {
          var ID = 'cofly-status-bar';

          var PALETTE = {
            normal: '#0a7d55',
            approaching: '#b8860b',
            reached: '#b3261e',
            unknown: '#5f6368'
          };

          function ensure() {
            var el = document.getElementById(ID);
            if (!el) {
              el = document.createElement('div');
              el.id = ID;
              el.setAttribute('aria-hidden', 'true');
              el.style.cssText = [
                'position:fixed', 'top:0', 'right:0', 'z-index:2147483647',
                'padding:3px 9px', 'font:12px/1.45 system-ui,sans-serif',
                'color:#fff', 'background:' + PALETTE.unknown,
                'border-bottom-left-radius:7px', 'pointer-events:none',
                'user-select:none', 'letter-spacing:.2px',
                'box-shadow:0 1px 4px rgba(0,0,0,.18)'
              ].join(';');
              (document.body || document.documentElement).appendChild(el);
            }
            return el;
          }

          window.__cofly = window.__cofly || {};

          window.__cofly.render = function (state) {
            try {
              var el = ensure();
              var s = state || {};
              el.style.background = PALETTE[s.tone] || PALETTE.unknown;
              el.textContent = s.label ? String(s.label) : '共飞';
              el.title = s.detail ? String(s.detail) : '';
              return true;
            } catch (e) {
              return false;
            }
          };

          window.__cofly.remove = function () {
            try {
              var el = document.getElementById(ID);
              if (el && el.parentNode) el.parentNode.removeChild(el);
              return true;
            } catch (e) {
              return false;
            }
          };

          ensure();
          return true;
        })();
        """;
}
