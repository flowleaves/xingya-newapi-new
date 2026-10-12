/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
// A display summary only; this does not participate in registration risk matching.
export function registrationAgentSummary(agent: string): string {
  const platform =
    ['Android', 'iPhone', 'iPad', 'Windows NT', 'Macintosh', 'Linux'].find(
      (name) => agent.toLowerCase().includes(name.toLowerCase())
    ) || ''
  const browsers: [string, RegExp][] = [
    ['Edge', /(?:EdgA?|EdgiOS)\/([\d.]+)/i],
    ['Opera', /(?:OPR|Opera)\/([\d.]+)/i],
    ['QQ Browser', /MQQBrowser\/([\d.]+)/i],
    ['Firefox', /(?:Firefox|FxiOS)\/([\d.]+)/i],
    ['Chrome', /(?:Chrome|CriOS)\/([\d.]+)/i],
    ['Safari', /Version\/([\d.]+).*Safari/i],
  ]
  for (const [name, pattern] of browsers) {
    const match = pattern.exec(agent)
    if (match) {
      return [platform, `${name} ${match[1]}`].filter(Boolean).join(' · ')
    }
  }
  return agent
}
