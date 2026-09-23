self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));

self.addEventListener('push', event => {
  let data = {};
  try { data = event.data?.json() || {}; } catch { /* Still display a generic notification. */ }
  const text = (key, fallback, limit) => typeof data[key] === 'string' ? data[key].slice(0, limit) : fallback;
  event.waitUntil(self.registration.showNotification(text('title', 'Ticket', 100), {
    body: text('body', 'Open Ticket to check its status.', 300),
    tag: text('tag', 'ticket-readiness', 160),
    icon: '/pwa/icon-192.png', badge: '/pwa/icon-192.png'
  }));
});

self.addEventListener('notificationclick', event => {
  event.notification.close();
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    const ticket = windows.find(client => client.url === new URL('/', self.location.origin).href);
    if (ticket) return ticket.focus();
    return self.clients.openWindow('/');
  })());
});
