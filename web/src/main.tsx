import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { AuthProvider } from './auth/AuthContext';
import { HazardKindsProvider } from './state/HazardKinds';
import './styles.css';

const root = document.getElementById('root');
if (!root) throw new Error('missing #root');

createRoot(root).render(
  <StrictMode>
    <AuthProvider>
      <HazardKindsProvider>
        <App />
      </HazardKindsProvider>
    </AuthProvider>
  </StrictMode>,
);
