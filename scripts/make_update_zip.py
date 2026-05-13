from __future__ import annotations

import argparse
import os
import shutil
import zipfile
from datetime import datetime
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_PREFIX = "免费生图-更新包"
EXCLUDED_DIR_NAMES = {
    ".git",
    ".venv",
    "__pycache__",
    "media",
    "node_modules",
    "staticfiles",
}
EXCLUDED_FILE_NAMES = {
    ".DS_Store",
    "db.sqlite3",
}
EXCLUDED_SUFFIXES = {".pyc", ".pyo"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build a deployment update zip package.")
    parser.add_argument(
        "--output-dir",
        default=str(Path.home() / "Desktop"),
        help="Directory where the package folder and zip will be created.",
    )
    parser.add_argument(
        "--prefix",
        default=DEFAULT_PREFIX,
        help="Filename prefix for the generated package.",
    )
    return parser.parse_args()


def should_skip_dir(dir_name: str) -> bool:
    return dir_name in EXCLUDED_DIR_NAMES


def should_skip_file(file_path: Path) -> bool:
    if file_path.name in EXCLUDED_FILE_NAMES:
        return True
    if file_path.suffix in EXCLUDED_SUFFIXES:
        return True
    if file_path.suffix == ".zip":
        return True
    return False


def copy_project_tree(source_root: Path, target_root: Path) -> None:
    for current_root, dir_names, file_names in os.walk(source_root):
        current_path = Path(current_root)
        rel_dir = current_path.relative_to(source_root)

        dir_names[:] = [name for name in dir_names if not should_skip_dir(name)]

        destination_dir = target_root / rel_dir
        destination_dir.mkdir(parents=True, exist_ok=True)

        for file_name in file_names:
            source_file = current_path / file_name
            if should_skip_file(source_file):
                continue
            destination_file = destination_dir / file_name
            shutil.copy2(source_file, destination_file)


def build_zip(source_dir: Path, zip_path: Path) -> None:
    with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for file_path in sorted(source_dir.rglob("*")):
            arcname = Path(source_dir.name) / file_path.relative_to(source_dir)
            archive.write(file_path, arcname)


def main() -> None:
    args = parse_args()
    output_dir = Path(args.output_dir).expanduser().resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    package_name = f"{args.prefix}-{timestamp}"
    package_dir = output_dir / package_name
    zip_path = output_dir / f"{package_name}.zip"

    if package_dir.exists():
        shutil.rmtree(package_dir)
    if zip_path.exists():
        zip_path.unlink()

    package_dir.mkdir(parents=True, exist_ok=True)
    copy_project_tree(PROJECT_ROOT, package_dir)
    build_zip(package_dir, zip_path)

    print(f"PACKAGE_DIR={package_dir}")
    print(f"ZIP_PATH={zip_path}")


if __name__ == "__main__":
    main()
