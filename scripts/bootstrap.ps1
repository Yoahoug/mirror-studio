python -m venv .venv
. .\.venv\Scripts\Activate.ps1
pip install -r requirements/dev.txt
npm install
Set-Location frontend/workspace
npm install
Set-Location ../..
Copy-Item .env.example .env -Force
npm run build:css

