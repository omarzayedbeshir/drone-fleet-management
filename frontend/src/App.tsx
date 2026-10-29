import { useEffect, useState } from 'react';

export function App() {
  const [status, setStatus] = useState('Checking API connection…');

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 5000);
    async function checkHealth() {
      try {
        const response = await fetch('/api/v1/health', { signal: controller.signal });
        if (!response.ok) throw new Error('Health check failed');
        const health: unknown = await response.json();
        if (typeof health !== 'object' || health === null || !('status' in health) || health.status !== 'ok') {
          throw new Error('Unexpected health response');
        }
        if (active) setStatus('API connected');
      } catch {
        if (active) setStatus('API unavailable. Start the backend and refresh to reconnect.');
      } finally {
        window.clearTimeout(timeout);
      }
    }
    void checkHealth();
    return () => { active = false; controller.abort(); window.clearTimeout(timeout); };
  }, []);

  return (
    <main>
      <p className="eyebrow">Drone Fleet Management</p>
      <h1>AeroSync</h1>
      <p className="intro">One workspace for your mixed-vendor drone fleet.</p>
      <p className="status" role="status">{status}</p>
      <section aria-labelledby="next-heading">
        <h2 id="next-heading">Foundation ready</h2>
        <p>Fleet monitoring, mission scheduling, and maintenance workflows will be built here.</p>
      </section>
    </main>
  );
}
