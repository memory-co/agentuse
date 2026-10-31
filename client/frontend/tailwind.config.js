import animate from 'tailwindcss-animate';

export default {
  darkMode: ['class'],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: {
    colors: {
      border: 'hsl(var(--border))', input: 'hsl(var(--input))', ring: 'hsl(var(--ring))',
      background: 'hsl(var(--background))', foreground: 'hsl(var(--foreground))',
      ...Object.fromEntries(['primary', 'secondary', 'destructive', 'muted', 'accent', 'popover', 'card'].map(name => [name, { DEFAULT: `hsl(var(--${name}))`, foreground: `hsl(var(--${name}-foreground))` }])),
      sidebar: { DEFAULT: 'hsl(var(--sidebar-background))', foreground: 'hsl(var(--sidebar-foreground))', primary: 'hsl(var(--sidebar-primary))', 'primary-foreground': 'hsl(var(--sidebar-primary-foreground))', accent: 'hsl(var(--sidebar-accent))', 'accent-foreground': 'hsl(var(--sidebar-accent-foreground))', border: 'hsl(var(--sidebar-border))', ring: 'hsl(var(--sidebar-ring))' },
    },
    borderRadius: { lg: 'var(--radius)', md: 'calc(var(--radius) - 2px)', sm: 'calc(var(--radius) - 4px)' },
  } },
  plugins: [animate],
};
