import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app/App'
import './app/styles.css'

const root = document.getElementById('root')
if (!root) throw new Error('控制台入口缺失。')

createRoot(root).render(<StrictMode><App /></StrictMode>)
