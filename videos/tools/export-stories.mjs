// Vuelca los guiones a JSON para las herramientas en Python.
import { stories } from '../src/stories.mjs'
process.stdout.write(JSON.stringify(stories))
