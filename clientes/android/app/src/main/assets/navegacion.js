// Se inyecta en la web de Velora sin cambiarla. En todos los aparatos: botón Atrás y pantalla encendida mientras suena un vídeo.
// En TV: moverse con las flechas del mando entre lo que se puede seleccionar, sin cursor. La web marca con data-tv-omitir lo que
// el mando se salta (títulos, botones de la carátula): sus acciones están en el menú, con la tecla Menú o manteniendo OK.
(function () {
  if (window.__veloraNavegacion) return;
  window.__veloraNavegacion = true;

  function abierto() {
    return document.querySelector('[role="dialog"],[role="alertdialog"],[role="menu"],[aria-modal="true"]');
  }

  window.__veloraAtras = function () {
    if (!abierto()) return false;
    // Desde el elemento con el foco: los menús de la web escuchan en sí mismos y los diálogos en el documento, y así llega a ambos.
    (document.activeElement || document).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    return true;
  };

  if (window.VeloraApp) {
    var sonando = function () {
      var hay = Array.prototype.some.call(document.querySelectorAll('video'), function (v) { return !v.paused && !v.ended; });
      VeloraApp.mantenerPantalla(hay);
    };
    ['play', 'playing', 'pause', 'ended', 'emptied'].forEach(function (e) { document.addEventListener(e, sonando, true); });
  }

  if (!window.__VELORA_TV) return;

  var estilo = document.createElement('style');
  estilo.textContent = '.velora-foco,*:focus{outline:3px solid #fff!important;outline-offset:2px!important;}';
  document.head.appendChild(estilo);

  var SELECCIONABLES = 'a[href],button,input:not([type=hidden]),select,textarea,summary,[tabindex]:not([tabindex="-1"]),'
    + '[role=button],[role=link],[role=tab],[role=menuitem],[role=option],[role=switch],[role=checkbox],[role=slider]';
  var FLECHAS = { ArrowUp: 1, ArrowDown: 1, ArrowLeft: 1, ArrowRight: 1 };
  var OK_LARGO_MS = 600;
  var ultimoPrincipal = null;

  function visible(el) {
    if (el.disabled) return false;
    var r = el.getBoundingClientRect();
    if (r.width < 2 || r.height < 2) return false;
    var c = getComputedStyle(el);
    return c.visibility !== 'hidden' && c.display !== 'none' && c.pointerEvents !== 'none';
  }

  function omitido(el) {
    return el.getAttribute('tabindex') === '-1' || el.closest('[data-tv-omitir]');
  }

  function candidatos() {
    var zona = abierto();
    var lista = zona ? Array.prototype.filter.call(zona.querySelectorAll(SELECCIONABLES), visible) : [];
    if (!lista.length) lista = Array.prototype.filter.call(document.querySelectorAll(SELECCIONABLES), function (el) { return !omitido(el) && visible(el); });
    return lista;
  }

  // El menú lateral y el contenido son dos zonas: solo se cruza con izquierda y derecha.
  function enLateral(el) {
    return !!el.closest('aside');
  }

  function actual() {
    var a = document.activeElement;
    return a && a !== document.body && a !== document.documentElement && a.matches(SELECCIONABLES) && visible(a) ? a : null;
  }

  // El más cercano en esa dirección: lo que está en línea pesa mucho más que lo que queda en diagonal.
  function siguiente(desde, flecha) {
    var o = desde.getBoundingClientRect();
    var mejor = null, puntos = Infinity;
    var vertical = flecha === 'ArrowUp' || flecha === 'ArrowDown';
    candidatos().forEach(function (el) {
      if (el === desde || el.contains(desde) || desde.contains(el)) return;
      if (!abierto() && (vertical || flecha === 'ArrowLeft') && enLateral(el) !== enLateral(desde)) return;
      var r = el.getBoundingClientRect(), distancia, cruce;
      if (flecha === 'ArrowUp') { distancia = o.top - r.bottom; cruce = Math.max(0, Math.max(o.left, r.left) < Math.min(o.right, r.right) ? 0 : Math.min(Math.abs(r.left - o.right), Math.abs(o.left - r.right))); }
      else if (flecha === 'ArrowDown') { distancia = r.top - o.bottom; cruce = Math.max(0, Math.max(o.left, r.left) < Math.min(o.right, r.right) ? 0 : Math.min(Math.abs(r.left - o.right), Math.abs(o.left - r.right))); }
      else if (flecha === 'ArrowLeft') { distancia = o.left - r.right; cruce = Math.max(o.top, r.top) < Math.min(o.bottom, r.bottom) ? 0 : Math.min(Math.abs(r.top - o.bottom), Math.abs(o.top - r.bottom)); }
      else { distancia = r.left - o.right; cruce = Math.max(o.top, r.top) < Math.min(o.bottom, r.bottom) ? 0 : Math.min(Math.abs(r.top - o.bottom), Math.abs(o.top - r.bottom)); }
      if (distancia < -2) return;
      var total = Math.max(distancia, 0) + cruce * 3;
      if (total < puntos) { puntos = total; mejor = el; }
    });
    return mejor;
  }

  function primero() {
    var todos = candidatos();
    var principal = todos.filter(function (el) { return !enLateral(el); });
    var lista = principal.length ? principal : todos;
    var enPantalla = lista.filter(function (el) {
      var r = el.getBoundingClientRect();
      return r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth;
    });
    return enPantalla[0] || lista[0] || null;
  }

  function enfocar(el) {
    el.focus({ preventScroll: true });
    // El lateral no debe mover la página que hay al lado.
    if (enLateral(el)) { el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' }); return; }
    ultimoPrincipal = el;
    el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
  }

  function recordado() {
    return ultimoPrincipal && ultimoPrincipal.isConnected && visible(ultimoPrincipal) && !omitido(ultimoPrincipal) ? ultimoPrincipal : null;
  }

  // Salir del contenido lleva a la sección abierta del lateral; volver, a donde se estaba.
  function cruzar(desde, flecha) {
    if (abierto()) return null;
    if (flecha === 'ArrowLeft' && !enLateral(desde)) return document.querySelector('aside [aria-current="page"]') || candidatos().filter(enLateral)[0] || null;
    if (flecha === 'ArrowRight' && enLateral(desde)) return recordado() || primero();
    return null;
  }

  function mover(flecha) {
    var a = actual();
    var destino = !a ? recordado() || primero()
      : flecha === 'ArrowRight' && enLateral(a) && !abierto() ? cruzar(a, flecha)
      : siguiente(a, flecha) || cruzar(a, flecha);
    if (destino) { enfocar(destino); return true; }
    if (flecha === 'ArrowUp' || flecha === 'ArrowDown') scrollBy({ top: (flecha === 'ArrowUp' ? -0.4 : 0.4) * innerHeight, behavior: 'instant' });
    return false;
  }

  // En un campo de texto, las flechas laterales mueven el cursor del texto hasta llegar al borde.
  function dentroDeTexto(el, flecha) {
    if (!el) return false;
    if (el.tagName === 'SELECT') return flecha === 'ArrowUp' || flecha === 'ArrowDown';
    if (el.tagName === 'INPUT' && el.type === 'range') return flecha === 'ArrowLeft' || flecha === 'ArrowRight';
    var texto = el.tagName === 'TEXTAREA' || (el.tagName === 'INPUT' && /^(text|search|email|password|url|tel|number)$/.test(el.type));
    if (!texto) return false;
    if (flecha !== 'ArrowLeft' && flecha !== 'ArrowRight') return el.tagName === 'TEXTAREA';
    try {
      return flecha === 'ArrowLeft' ? el.selectionStart > 0 : el.selectionEnd < el.value.length;
    } catch (noAdmite) { return false; }
  }

  function reproductor() {
    return document.querySelector('video');
  }

  function revelarControles(v) {
    (v.parentElement || v).dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: 5 + Math.random(), clientY: 5 }));
  }

  // Reproductor: fase de captura, porque la web usa las flechas para saltar y para el volumen.
  // Sin control seleccionado: izquierda y derecha saltan (lo hace la web), arriba y abajo llevan a los controles, OK pausa.
  // Con un control seleccionado: las flechas se mueven entre controles y OK lo pulsa.
  window.addEventListener('keydown', function (e) {
    var v = reproductor();
    if (!v || abierto()) return;
    revelarControles(v);
    var a = actual();
    if (FLECHAS[e.key]) {
      if (a && dentroDeTexto(a, e.key)) { e.stopPropagation(); return; }
      if (!a && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) return;
      e.preventDefault();
      e.stopPropagation();
      setTimeout(function () { mover(e.key); }, 40); // Los controles aparecen un instante después de revelarlos.
    } else if (e.key === 'Enter') {
      e.stopPropagation();
      if (a) return;
      e.preventDefault();
      document.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true }));
    }
  }, true);

  // El menú de la carátula seleccionada: tecla Menú (la avisa la app) o mantener OK.
  window.__veloraMenu = function () {
    var a = actual();
    if (!a || abierto() || reproductor() || !a.closest('[data-tv-tarjeta]')) return false;
    a.dispatchEvent(new KeyboardEvent('keydown', { key: 'ContextMenu', bubbles: true, cancelable: true }));
    return true;
  };

  // OK sobre un campo de texto: el mando no toca la pantalla, así que el teclado lo tiene que pedir la app.
  window.__veloraTeclado = function () {
    var a = actual();
    if (window.VeloraApp && a && (a.tagName === 'TEXTAREA' || (a.tagName === 'INPUT' && /^(text|search|email|password|url|tel|number)$/.test(a.type)))) VeloraApp.mostrarTeclado();
  };

  // En una carátula, OK se decide al soltar: una pulsación corta la abre y una larga saca su menú.
  var ok = null;
  window.addEventListener('keydown', function (e) {
    if (e.key !== 'Enter') return;
    if (ok) { e.preventDefault(); e.stopPropagation(); return; }
    var a = actual();
    if (!a || abierto() || reproductor() || !a.closest('[data-tv-tarjeta]')) return;
    e.preventDefault();
    e.stopPropagation();
    var pulsacion = { el: a, larga: false };
    pulsacion.reloj = setTimeout(function () { pulsacion.larga = true; window.__veloraMenu(); }, OK_LARGO_MS);
    ok = pulsacion;
  }, true);
  window.addEventListener('keyup', function (e) {
    if (e.key !== 'Enter' || !ok) return;
    var pulsacion = ok;
    ok = null;
    clearTimeout(pulsacion.reloj);
    e.preventDefault();
    e.stopPropagation();
    if (!pulsacion.larga && pulsacion.el.isConnected) pulsacion.el.click();
  }, true);

  // Resto de pantallas: fase de burbuja, para que los menús de la web conserven su propio manejo de flechas.
  window.addEventListener('keydown', function (e) {
    if (reproductor() && !abierto()) return;
    if (e.defaultPrevented) return;
    if (FLECHAS[e.key]) {
      if (dentroDeTexto(actual(), e.key)) return;
      e.preventDefault();
      mover(e.key);
    } else if (e.key === 'Enter') {
      var a = actual();
      if (!a) { e.preventDefault(); mover('ArrowDown'); return; }
      if (a && !/^(A|BUTTON|INPUT|SELECT|TEXTAREA|SUMMARY)$/.test(a.tagName)) { e.preventDefault(); a.click(); }
    }
  });
})();
