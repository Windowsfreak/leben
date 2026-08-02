<?php
session_start();
header('Content-Type: application/json');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';

// All asset admin actions require authentication
$is_admin = !empty($_SESSION['admin_logged_in']);
if (!$is_admin) {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Please log in first.']);
    exit;
}

try {
    switch ($action) {
        case 'list_assets':
            $dir = __DIR__ . '/assets';
            $include_data = !empty($_GET['include_data']);
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }
            $files = glob($dir . '/*');
            $assets = [];
            foreach ($files as $file) {
                if (is_file($file) && basename($file) !== '.gitignore') {
                    $item = [
                        'name' => basename($file),
                        'size' => filesize($file),
                        'mtime' => filemtime($file),
                        'url' => './assets/' . basename($file)
                    ];
                    if ($include_data) {
                        $item['data_b64'] = base64_encode(file_get_contents($file));
                    }
                    $assets[] = $item;
                }
            }
            usort($assets, function($a, $b) {
                return strcasecmp($a['name'], $b['name']);
            });
            echo json_encode([
                'status' => 'success',
                'assets' => $assets
            ]);
            break;

        case 'upload_asset':
            $file_param = $_FILES['asset'] ?? ($_FILES['file'] ?? ($_FILES['image'] ?? null));
            $overwrite = isset($_POST['overwrite']) && ($_POST['overwrite'] === 'true' || $_POST['overwrite'] === '1' || $_POST['overwrite'] === true);
            if (!$file_param || $file_param['error'] !== UPLOAD_ERR_OK) {
                throw new Exception("No asset uploaded or upload error occurred.");
            }

            $tmpPath = $file_param['tmp_name'];
            $origName = $file_param['name'];

            $dir = __DIR__ . '/assets';
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }

            $baseName = pathinfo($origName, PATHINFO_FILENAME);
            $origExt = strtolower(pathinfo($origName, PATHINFO_EXTENSION));

            $cleanBase = preg_replace('/[^a-zA-Z0-9_\-]/', '_', $baseName);
            $cleanExt = preg_replace('/[^a-zA-Z0-9]/', '', $origExt);

            if (empty($cleanExt)) {
                throw new Exception("Asset extension is missing or invalid.");
            }

            $finalName = $cleanBase . '.' . $cleanExt;
            if (!$overwrite) {
                $counter = 1;
                while (file_exists($dir . '/' . $finalName)) {
                    $finalName = $cleanBase . '_' . $counter . '.' . $cleanExt;
                    $counter++;
                }
            }

            $targetPath = $dir . '/' . $finalName;
            if (!move_uploaded_file($tmpPath, $targetPath)) {
                throw new Exception("Failed to move uploaded asset file.");
            }

            echo json_encode([
                'status' => 'success',
                'message' => 'Asset uploaded successfully.',
                'asset' => [
                    'name' => $finalName,
                    'size' => filesize($targetPath),
                    'mtime' => filemtime($targetPath),
                    'url' => './assets/' . $finalName
                ]
            ]);
            break;

        case 'rename_asset':
            $oldName = trim($_POST['old_name'] ?? '');
            $newName = trim($_POST['new_name'] ?? '');
            if (empty($oldName) || empty($newName)) {
                throw new Exception("Old and new asset filenames are required.");
            }

            $oldExt = strtolower(pathinfo($oldName, PATHINFO_EXTENSION));
            $newNameBase = pathinfo($newName, PATHINFO_FILENAME);
            $newNameClean = preg_replace('/[^a-zA-Z0-9_\-]/', '_', $newNameBase) . '.' . $oldExt;

            $oldPath = __DIR__ . '/assets/' . $oldName;
            $newPath = __DIR__ . '/assets/' . $newNameClean;

            if (!file_exists($oldPath)) {
                throw new Exception("Source asset file does not exist.");
            }
            if (file_exists($newPath)) {
                throw new Exception("Target asset filename already exists.");
            }

            if (!rename($oldPath, $newPath)) {
                throw new Exception("Failed to rename asset file on disk.");
            }

            echo json_encode([
                'status' => 'success',
                'message' => 'Asset renamed successfully.',
                'new_name' => $newNameClean
            ]);
            break;

        case 'delete_asset':
            $name = trim($_POST['name'] ?? '');
            if (empty($name)) {
                throw new Exception("Asset filename is required.");
            }

            $path = __DIR__ . '/assets/' . $name;
            if (!file_exists($path)) {
                throw new Exception("Asset file does not exist.");
            }

            if (!unlink($path)) {
                throw new Exception("Failed to delete asset file from disk.");
            }

            echo json_encode([
                'status' => 'success',
                'message' => 'Asset deleted successfully.'
            ]);
            break;

        default:
            throw new Exception("Invalid asset administration action.");
    }

} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
