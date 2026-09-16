import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import 'normalize.css'
import './index.css'
import App from './App.tsx'
import Sweep from './game/Sweep.tsx'

// ?sweep is the M-A harness (01 Thesis/Measurement Methodology.md) and it drives
// the sim itself, so it replaces the game rather than running beside it: a render
// loop advancing frames underneath it would time something else entirely. Chosen
// here rather than inside App because a page is a page, not a branch in one.
const page = new URLSearchParams(location.search).has('sweep') ? <Sweep /> : <App />

createRoot(document.getElementById('root')!).render(<StrictMode>{page}</StrictMode>)
