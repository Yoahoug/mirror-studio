import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MotionConfig } from 'motion/react';
import { ToastProvider } from '@shared/ui';
import { LanguageProvider } from '@shared/i18n';
import App from './App.tsx';
import './index.css';

// Support both development (root) and production (console-root) mount points
const rootEl = document.getElementById('console-root') || document.getElementById('root');

if (rootEl) {
  createRoot(rootEl).render(
    <StrictMode>
      <MotionConfig reducedMotion="user">
        <LanguageProvider>
          <ToastProvider>
            <App />
          </ToastProvider>
        </LanguageProvider>
      </MotionConfig>
    </StrictMode>,
  );
}
