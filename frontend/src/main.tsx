import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import App from './App'
import { trackWindowFocus } from './windowFocus'

const stopTrackingFocus = trackWindowFocus()
if (import.meta.hot) import.meta.hot.dispose(stopTrackingFocus)

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <App/>
    </React.StrictMode>
)
