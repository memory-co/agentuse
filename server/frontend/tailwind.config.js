import animate from 'tailwindcss-animate';

const color = name => `hsl(var(--${name}) / <alpha-value>)`;

export default {
  darkMode: ['class'],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: {
    colors: {
      border: color('border'), input: color('input'), ring: color('ring'),
      background: color('background'), foreground: color('foreground'), panel: color('panel'),
      ...Object.fromEntries(['primary', 'secondary', 'destructive', 'muted', 'accent', 'popover', 'card'].map(name => [name, { DEFAULT: color(name), foreground: color(`${name}-foreground`) }])),
      sidebar: { DEFAULT: color('sidebar-background'), foreground: color('sidebar-foreground'), primary: color('sidebar-primary'), 'primary-foreground': color('sidebar-primary-foreground'), accent: color('sidebar-accent'), 'accent-foreground': color('sidebar-accent-foreground'), border: color('sidebar-border'), ring: color('sidebar-ring') },
    },
    borderRadius: { lg: 'var(--radius)', md: 'calc(var(--radius) - 2px)', sm: 'calc(var(--radius) - 4px)' },
  } },
  plugins: [animate],
};
