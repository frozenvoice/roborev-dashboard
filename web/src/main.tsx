import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App, ErrorBoundary } from './App'
import { I18nProvider } from './i18n'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider><ErrorBoundary><App /></ErrorBoundary></I18nProvider>
  </StrictMode>,
)
