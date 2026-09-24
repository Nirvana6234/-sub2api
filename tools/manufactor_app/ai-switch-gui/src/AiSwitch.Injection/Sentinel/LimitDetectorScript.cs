namespace LanAi.Workspace.Injection.Sentinel;

/// <summary>
/// The in-page half of the limit sentinel.
/// </summary>
/// <remarks>
/// Measured facts that shape this script (verified against the real app, 2026-07-26):
///
/// 1. <b>The UI is localized.</b> On a Chinese install the visible text is
///    "你已达到使用上限。…", never the English "hit your usage limit" that the bundle
///    also ships. Matching English only would make the sentinel silently blind, so
///    the patterns below cover the strings actually extracted from the app bundle
///    (zh-CN, zh-TW, ja, en).
/// 2. <b>Similar wording exists for unrelated limits.</b> "你已達邀請上限" is a
///    workspace <i>invite</i> cap and "目標已達成" is a goal notice; a loose
///    /已達.*上限/ would fire on both. Every pattern therefore requires the limit to
///    be a usage/quota limit.
/// 3. <b>There is no shadow DOM</b> — the app renders into the light DOM
///    (1236 elements, 0 shadow hosts observed). The traversal still recurses through
///    <c>shadowRoot</c> so a future re-architecture does not blind the sentinel, but
///    nothing depends on finding one.
/// 4. The bundle module names (<c>rateLimitResetModal</c>,
///    <c>rateLimitResetHomeBanner</c>) are matched against element attributes as a
///    locale-independent bonus signal. <b>They are build-chunk names, not confirmed
///    DOM class names</b>, so they may never match; text remains the primary signal
///    until a real limited state can be observed.
///
/// The script reports <b>facts only</b> — which surface is present, which text
/// matched, what percentage was found. Deciding "approaching" versus "reached" is
/// left to <see cref="CodexLimitSentinel"/> so that the policy is unit testable
/// instead of buried in the page.
///
/// Page text never leaves the page: only booleans and a short "resets…" fragment are
/// returned, never conversation content.
///
/// It must never disturb the host app: every entry point is wrapped, the traversal
/// is node-capped, and rescans are debounced.
/// </remarks>
internal static class LimitDetectorScript
{
    /// <summary>
    /// Bump whenever <see cref="Source"/> changes behaviour. A page that already holds
    /// an older detector reinstalls; one that holds this version just rescans. Without
    /// the comparison an updated detector would never replace an old one in a
    /// long-lived document.
    /// </summary>
    internal const int Version = 2;

    internal const string Source = """
        (function () {
          var NS = '__coflySentinel';
          var VERSION = 2;

          if (window[NS] && window[NS].version === VERSION) {
            try { window[NS].rescan(); } catch (e) { }
            return true;
          }

          // An older detector is present: shut its observers down before replacing it,
          // otherwise they keep firing against state nothing reads.
          if (window[NS] && typeof window[NS].stop === 'function') {
            try { window[NS].stop(); } catch (e) { }
          }

          var MAX_NODES = 20000;
          var DEBOUNCE_MS = 250;

          // Anchors taken from the official bundle names.
          var SURFACE = {
            modal: /rateLimitReset(Prompt)?Modal|rate-limit-reset(-prompt)?-modal/i,
            banner: /rateLimitResetHomeBanner|rate-limit-reset-home-banner/i,
            generic: /rate.?limit/i
          };
          // Strings verified by extracting them from the app bundle. Each "reached"
          // pattern names the resource being capped so that invite caps
          // ("你已達邀請上限") and goal notices ("目標已達成") cannot trigger it.
          var TEXT = {
            reached: [
              /hit your usage limit/i,
              /usage limit reached/i,
              /you(?:'ve| have) reached your usage limit/i,
              /(?:已达到|已達到|已达|已達)\s*(?:使用|用量|额度|額度)上限/,
              /(?:使用|用量|额度|額度)上限[^。\n]{0,6}(?:已达|已達)/,
              /利用上限に達しました/
            ],
            usageLimits: [/usage limits?/i, /(?:使用|用量)上限/, /用量限制/],
            resets: [
              /resets?\s+(?:at|in|on)\b[^.\n]{0,48}/i,
              /你的(?:额度|額度|速率限制)(?:将|將)[於于][^。\n]{0,32}/,
              /(?:用量|额度|額度|速率限制)重(?:置|設)[^。\n]{0,32}/
            ],
            // Anything matching this is a different cap and must not count as usage.
            notUsage: /(?:邀請|邀请)上限|目標已達成|目标已达成/
          };

          function anyMatch(patterns, text) {
            for (var i = 0; i < patterns.length; i++) {
              if (patterns[i].test(text)) return true;
            }
            return false;
          }

          function firstMatch(patterns, text) {
            for (var i = 0; i < patterns.length; i++) {
              var m = text.match(patterns[i]);
              if (m) return m[0];
            }
            return null;
          }

          // Exposed so the pattern set can be validated against known-good and
          // known-bad samples in a real engine, without reading page content.
          function matchText(sample) {
            var s = String(sample || '');
            return {
              reached: anyMatch(TEXT.reached, s) && !TEXT.notUsage.test(s),
              usageLimits: anyMatch(TEXT.usageLimits, s),
              resetText: firstMatch(TEXT.resets, s),
              excludedAsOtherLimit: TEXT.notUsage.test(s)
            };
          }

          var state = null;
          var lastReachedAt = null;
          var observed = typeof WeakSet === 'function' ? new WeakSet() : null;
          var observers = [];
          var timer = null;
          var stopped = false;

          function attrBag(el) {
            var bag = '';
            try {
              if (el.className && el.className.toString) bag += ' ' + el.className.toString();
              if (el.id) bag += ' ' + el.id;
              var t = el.getAttribute && (el.getAttribute('data-testid') || el.getAttribute('data-test-id'));
              if (t) bag += ' ' + t;
              var a = el.getAttribute && el.getAttribute('aria-label');
              if (a) bag += ' ' + a;
            } catch (e) { }
            return bag;
          }

          function visible(el) {
            try {
              var r = el.getBoundingClientRect();
              return r.width > 0 && r.height > 0;
            } catch (e) { return false; }
          }

          function percentFrom(el) {
            try {
              var role = el.getAttribute && el.getAttribute('role');
              if (role === 'progressbar') {
                var now = parseFloat(el.getAttribute('aria-valuenow'));
                var max = parseFloat(el.getAttribute('aria-valuemax'));
                if (!isNaN(now)) {
                  if (!isNaN(max) && max > 0) return Math.round((now / max) * 100);
                  return Math.round(now);
                }
              }
              var txt = (el.textContent || '');
              if (txt.length <= 120 && SURFACE.generic.test(attrBag(el) + ' ' + txt)) {
                var m = txt.match(/(\d{1,3})\s*%/);
                if (m) {
                  var v = parseInt(m[1], 10);
                  if (v >= 0 && v <= 100) return v;
                }
              }
            } catch (e) { }
            return null;
          }

          // Walks the light DOM and every shadow root, invoking visit(element).
          function deepWalk(visit) {
            var roots = [document];
            var seenRoots = 0;
            var nodes = 0;

            while (roots.length) {
              var root = roots.shift();
              seenRoots++;
              var list;
              try { list = root.querySelectorAll ? root.querySelectorAll('*') : []; }
              catch (e) { continue; }

              for (var i = 0; i < list.length; i++) {
                if (++nodes > MAX_NODES) return { nodes: nodes, roots: seenRoots, capped: true };
                var el = list[i];
                try { visit(el); } catch (e) { }
                if (el.shadowRoot) roots.push(el.shadowRoot);
              }
            }
            return { nodes: nodes, roots: seenRoots, capped: false };
          }

          function observe(root) {
            if (stopped || !observed || observed.has(root)) return;
            if (typeof MutationObserver !== 'function') return;
            try {
              observed.add(root);
              var mo = new MutationObserver(schedule);
              mo.observe(root, {
                childList: true, subtree: true, characterData: true,
                attributes: true, attributeFilter: ['class', 'aria-valuenow', 'aria-label']
              });
              observers.push(mo);
            } catch (e) { }
          }

          function stop() {
            stopped = true;
            if (timer) { try { clearTimeout(timer); } catch (e) { } timer = null; }
            for (var i = 0; i < observers.length; i++) {
              try { observers[i].disconnect(); } catch (e) { }
            }
            observers = [];
          }

          function scan() {
            var signals = [];
            var modal = false, banner = false, percent = null, resetText = null;
            var reached = false, usageLimits = false;

            var walk = deepWalk(function (el) {
              var bag = attrBag(el);
              if (bag) {
                if (SURFACE.modal.test(bag) && visible(el)) {
                  modal = true;
                  signals.push('surface:modal');
                } else if (SURFACE.banner.test(bag) && visible(el)) {
                  banner = true;
                  signals.push('surface:banner');
                }
              }
              if (percent === null) {
                var p = percentFrom(el);
                if (p !== null) { percent = p; signals.push('percent:' + p); }
              }
              if (el.shadowRoot) observe(el.shadowRoot);
            });

            // Text is read from the deepest roots too, so concatenate root-level text.
            var text = '';
            try {
              text = (document.body ? document.body.innerText : '') || '';
              deepWalk(function (el) {
                if (el.shadowRoot && text.length < 20000) {
                  text += ' ' + (el.shadowRoot.textContent || '');
                }
              });
            } catch (e) { }

            var verdict = matchText(text);
            if (verdict.reached) { reached = true; signals.push('text:reached'); }
            if (verdict.usageLimits) { usageLimits = true; signals.push('text:usageLimits'); }
            if (verdict.resetText) {
              resetText = verdict.resetText.trim().slice(0, 80);
              signals.push('text:resets');
            }

            if (reached || modal) lastReachedAt = Date.now();

            state = {
              version: VERSION,
              modal: modal,
              banner: banner,
              reachedText: reached,
              usageLimitsText: usageLimits,
              resetText: resetText,
              percent: percent,
              signals: signals.slice(0, 12),
              scannedAt: Date.now(),
              lastReachedAt: lastReachedAt,
              nodes: walk.nodes,
              roots: walk.roots,
              capped: walk.capped
            };
            return state;
          }

          function schedule() {
            if (stopped || timer) return;
            timer = setTimeout(function () {
              timer = null;
              try { scan(); } catch (e) { }
            }, DEBOUNCE_MS);
          }

          window[NS] = {
            version: VERSION,
            stop: stop,
            rescan: function () { try { return scan(); } catch (e) { return null; } },
            snapshot: function () {
              try { return state || scan(); } catch (e) { return null; }
            },
            matchText: function (sample) {
              try { return matchText(sample); } catch (e) { return null; }
            }
          };

          observe(document);
          try { scan(); } catch (e) { }
          return true;
        })();
        """;
}
