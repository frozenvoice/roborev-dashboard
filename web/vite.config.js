import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig({
    plugins: [react()],
    build: {
        outDir: '../internal/webui/dist',
        emptyOutDir: false,
        sourcemap: false,
    },
    test: {
        environment: 'jsdom',
        setupFiles: './src/test-setup.ts',
        css: true,
    },
});
