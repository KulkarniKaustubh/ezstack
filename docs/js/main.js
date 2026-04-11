/* ========================================
   ezstack — Landing Page Scripts
   ======================================== */

(function () {
  'use strict';

  // ── Navigation ──────────────────────────

  var nav = document.getElementById('nav');
  var navToggle = document.getElementById('nav-toggle');
  var navLinks = document.getElementById('nav-links');

  // Scroll: add background to nav
  function onScroll() {
    if (window.scrollY > 40) {
      nav.classList.add('scrolled');
    } else {
      nav.classList.remove('scrolled');
    }
  }
  window.addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // Mobile hamburger toggle
  navToggle.addEventListener('click', function () {
    navToggle.classList.toggle('active');
    navLinks.classList.toggle('open');
  });

  // Close mobile nav on link click
  var navAnchors = navLinks.querySelectorAll('a');
  for (var i = 0; i < navAnchors.length; i++) {
    navAnchors[i].addEventListener('click', function () {
      navToggle.classList.remove('active');
      navLinks.classList.remove('open');
    });
  }

  // Nav dropdown toggle
  var dropdowns = document.querySelectorAll('.nav-dropdown');
  dropdowns.forEach(function (dd) {
    var toggle = dd.querySelector('.nav-dropdown-toggle');
    toggle.addEventListener('click', function (e) {
      e.stopPropagation();
      var wasOpen = dd.classList.contains('open');
      // Close all dropdowns first
      dropdowns.forEach(function (d) { d.classList.remove('open'); });
      if (!wasOpen) dd.classList.add('open');
    });
  });
  // Close dropdown on outside click
  document.addEventListener('click', function () {
    dropdowns.forEach(function (d) { d.classList.remove('open'); });
  });

  // Smooth scroll for internal links
  document.addEventListener('click', function (e) {
    var target = e.target.closest('a[href^="#"]');
    if (!target) return;
    var id = target.getAttribute('href');
    if (id === '#') return;
    var el = document.querySelector(id);
    if (el) {
      e.preventDefault();
      el.scrollIntoView({ behavior: 'smooth' });
    }
  });

  // ── Scroll Animations ──────────────────

  var animateEls = document.querySelectorAll('[data-animate]');
  var observer = new IntersectionObserver(
    function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('visible');
          observer.unobserve(entry.target);
        }
      });
    },
    { threshold: 0.1, rootMargin: '0px 0px -40px 0px' }
  );

  animateEls.forEach(function (el) {
    observer.observe(el);
  });

  // ── Copy to Clipboard ──────────────────

  document.addEventListener('click', function (e) {
    var btn = e.target.closest('.copy-btn');
    if (!btn) return;
    var text = btn.getAttribute('data-copy');
    if (!text) return;

    navigator.clipboard.writeText(text).then(function () {
      var label = btn.querySelector('.copy-label');
      var originalText = label.textContent;
      btn.classList.add('copied');
      label.textContent = 'Copied!';
      setTimeout(function () {
        btn.classList.remove('copied');
        label.textContent = originalText;
      }, 2000);
    });
  });

  // ── Accordion ──────────────────────────

  var accordionHeaders = document.querySelectorAll('.accordion-header');
  accordionHeaders.forEach(function (header) {
    header.addEventListener('click', function () {
      var item = header.parentElement;
      var isOpen = item.classList.contains('open');

      // Close all others
      document.querySelectorAll('.accordion-item.open').forEach(function (openItem) {
        openItem.classList.remove('open');
        openItem.querySelector('.accordion-header').setAttribute('aria-expanded', 'false');
      });

      // Toggle this one
      if (!isOpen) {
        item.classList.add('open');
        header.setAttribute('aria-expanded', 'true');
      }
    });
  });

  // ── Install Tabs ───────────────────────

  var tabBtns = document.querySelectorAll('.tab-btn');
  tabBtns.forEach(function (btn) {
    btn.addEventListener('click', function () {
      var tabId = btn.getAttribute('data-tab');

      // Deactivate all
      tabBtns.forEach(function (b) { b.classList.remove('active'); });
      document.querySelectorAll('.tab-panel').forEach(function (p) { p.classList.remove('active'); });

      // Activate selected
      btn.classList.add('active');
      var panel = document.getElementById('tab-' + tabId);
      if (panel) panel.classList.add('active');
    });
  });

  // ── Terminal Typing Animation ──────────

  var terminalContent = document.getElementById('terminal-content');
  var terminalCursor = document.getElementById('terminal-cursor');

  var demoScript = [
    { type: 'command', prompt: '$ ', text: 'ezs new auth-service' },
    { type: 'output', lines: [
      '<span class="t-dim">Created branch \'auth-service\' with worktree at ~/worktrees/auth-service</span>'
    ]},
    { type: 'pause', duration: 600 },
    { type: 'command', prompt: '$ ', text: 'ezs new auth-middleware --parent auth-service' },
    { type: 'output', lines: [
      '<span class="t-dim">Created branch \'auth-middleware\' with worktree at ~/worktrees/auth-middleware</span>'
    ]},
    { type: 'pause', duration: 600 },
    { type: 'command', prompt: '$ ', text: 'ezs ls' },
    { type: 'output', lines: [
      '  <span class="t-dim">main</span>',
      '  <span class="t-dim">\u2514\u2500\u2500</span> <span class="t-branch">auth-service</span>        <span class="t-info">PR #42</span> <span class="t-success">\u2713 2 approved</span>  <span class="t-success">+120</span> <span class="t-error">-14</span>',
      '  <span class="t-dim">    \u2514\u2500\u2500</span> <span class="t-branch">auth-middleware</span>  <span class="t-info">PR #43</span> <span class="t-dim">\u25cf CI running</span>   <span class="t-success">+45</span> <span class="t-error">-3</span>'
    ]},
    { type: 'pause', duration: 800 },
    { type: 'command', prompt: '$ ', text: 'ezs commit -m "Add middleware validation"' },
    { type: 'output', lines: [
      '<span class="t-dim">[auth-middleware abc1234] Add middleware validation</span>',
      '<span class="t-success">Syncing children... done.</span>'
    ]},
    { type: 'pause', duration: 600 },
    { type: 'command', prompt: '$ ', text: 'ezs sync -a' },
    { type: 'output', lines: [
      '<span class="t-success">\u2713</span> <span class="t-dim">auth-service is up to date</span>',
      '<span class="t-success">\u2713</span> <span class="t-dim">auth-middleware rebased onto auth-service</span>',
      '<span class="t-success">All stacks synced.</span>'
    ]},
  ];

  var currentLine = '';
  var scriptIndex = 0;
  var charIndex = 0;
  var isAnimating = false;

  function clearTerminal() {
    terminalContent.innerHTML = '';
    currentLine = '';
  }

  function appendToTerminal(html) {
    terminalContent.innerHTML += html;
    // Keep terminal scrolled to bottom
    var body = document.getElementById('terminal-body');
    body.scrollTop = body.scrollHeight;
  }

  function typeCommand(step, callback) {
    var fullText = step.text;
    var promptHtml = '<span class="t-prompt">' + step.prompt + '</span>';
    charIndex = 0;

    // Start a new line with prompt
    appendToTerminal(promptHtml);

    function typeNext() {
      if (charIndex < fullText.length) {
        var char = fullText[charIndex];
        // Color flags and strings
        var span;
        if (char === '-' && charIndex > 0 && fullText[charIndex - 1] === ' ') {
          // Find the end of the flag
          var flagEnd = fullText.indexOf(' ', charIndex);
          if (flagEnd === -1) flagEnd = fullText.length;
          var flag = fullText.substring(charIndex, flagEnd);
          appendToTerminal('<span class="t-flag">' + escapeHtml(flag) + '</span>');
          charIndex = flagEnd;
        } else if (char === '"') {
          // Find closing quote
          var closeQuote = fullText.indexOf('"', charIndex + 1);
          if (closeQuote !== -1) {
            var str = fullText.substring(charIndex, closeQuote + 1);
            appendToTerminal('<span class="t-str">' + escapeHtml(str) + '</span>');
            charIndex = closeQuote + 1;
          } else {
            appendToTerminal(escapeHtml(char));
            charIndex++;
          }
        } else {
          // Check if this is the command name (first word after prompt)
          if (charIndex === 0) {
            var cmdEnd = fullText.indexOf(' ', 4); // after "ezs "
            if (cmdEnd === -1) cmdEnd = fullText.length;
            var cmd = fullText.substring(0, cmdEnd);
            appendToTerminal('<span class="t-cmd">' + escapeHtml(cmd) + '</span>');
            charIndex = cmdEnd;
          } else {
            appendToTerminal(escapeHtml(char));
            charIndex++;
          }
        }
        setTimeout(typeNext, 30 + Math.random() * 20);
      } else {
        appendToTerminal('\n');
        callback();
      }
    }

    typeNext();
  }

  function showOutput(step, callback) {
    var lines = step.lines;
    var lineIndex = 0;

    function showNext() {
      if (lineIndex < lines.length) {
        appendToTerminal(lines[lineIndex] + '\n');
        lineIndex++;
        setTimeout(showNext, 80);
      } else {
        callback();
      }
    }
    showNext();
  }

  function runScript() {
    if (scriptIndex >= demoScript.length) {
      // Pause then restart
      terminalCursor.style.display = 'inline-block';
      setTimeout(function () {
        clearTerminal();
        scriptIndex = 0;
        terminalCursor.style.display = 'none';
        runScript();
      }, 4000);
      return;
    }

    var step = demoScript[scriptIndex];
    scriptIndex++;
    terminalCursor.style.display = 'none';

    if (step.type === 'command') {
      terminalCursor.style.display = 'inline-block';
      setTimeout(function () {
        terminalCursor.style.display = 'none';
        typeCommand(step, function () {
          runScript();
        });
      }, 400);
    } else if (step.type === 'output') {
      showOutput(step, function () {
        runScript();
      });
    } else if (step.type === 'pause') {
      setTimeout(runScript, step.duration);
    }
  }

  function escapeHtml(text) {
    var div = document.createElement('div');
    div.appendChild(document.createTextNode(text));
    return div.innerHTML;
  }

  // Start terminal animation when visible
  var terminalEl = document.querySelector('.terminal');
  var terminalObserver = new IntersectionObserver(
    function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting && !isAnimating) {
          isAnimating = true;
          terminalCursor.style.display = 'inline-block';
          setTimeout(runScript, 800);
          terminalObserver.unobserve(entry.target);
        }
      });
    },
    { threshold: 0.3 }
  );
  terminalObserver.observe(terminalEl);

})();
