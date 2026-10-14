// Apariciones al desplazarse ("reveal"), sin hidratar nada: dos scripts en línea diminutos que
// van en el HTML del servidor (layouts/default.vue) y no dependen del bundle de la app, para
// que funcionen también en las secciones `hydrate-never` y no esperen a que cargue Vue.
//
// 1. <head>: añade `rv` a <html> antes del primer pintado, solo si hay IntersectionObserver y
//    NO hay prefers-reduced-motion. Sin esa clase el CSS no oculta nada (motion.css). Si en
//    3 s el observador no se ha registrado (script bloqueado, error), quita la clase: el
//    contenido nunca se queda oculto.
// 2. Final del <body>: observa cada [data-reveal] y le pone `rv-in` al entrar en pantalla (una
//    sola vez). `window.__hfRv()` vuelve a buscar elementos nuevos tras navegar en el cliente
//    (plugin motion.client.ts). Si la persona activa reduced motion con la página abierta,
//    todo se muestra al instante.
//
// El orden de prioridad lo da el propio CSS: `--rv-i` en cada elemento escalona la entrada.

export const revealHeadScript = `(function(){try{var d=document.documentElement;if(!('IntersectionObserver' in window)||matchMedia('(prefers-reduced-motion: reduce)').matches)return;d.classList.add('rv');setTimeout(function(){if(!window.__hfRv)d.classList.remove('rv')},3000)}catch(e){}})()`

export const revealBodyScript = `(function(){try{var d=document.documentElement;if(!d.classList.contains('rv'))return;var io=new IntersectionObserver(function(es){for(var i=0;i<es.length;i++){if(es[i].isIntersecting){es[i].target.classList.add('rv-in');io.unobserve(es[i].target)}}},{rootMargin:'0px 0px -8% 0px'});var scan=function(){var els=document.querySelectorAll('[data-reveal]:not(.rv-in)');for(var i=0;i<els.length;i++)io.observe(els[i])};window.__hfRv=scan;scan();var mq=matchMedia('(prefers-reduced-motion: reduce)');if(mq.addEventListener)mq.addEventListener('change',function(e){if(e.matches)d.classList.remove('rv')})}catch(e){document.documentElement.classList.remove('rv')}})()`
