import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '../styles.css';
import './installer.css';
import InstallerApp from './App';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <InstallerApp />
  </StrictMode>,
);
