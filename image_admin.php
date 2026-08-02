<?php
session_start();
header('Content-Type: application/json');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';

// All image admin actions require authentication
$is_admin = !empty($_SESSION['admin_logged_in']);
if (!$is_admin) {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Please log in first.']);
    exit;
}

try {
    $db = get_db_connection();

    switch ($action) {
        case 'list_images':
            $dir = __DIR__ . '/tileimg';
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }
            $files = glob($dir . '/*.{webp,WEBP,png,PNG,jpg,jpeg,JPG,JPEG,gif,GIF,svg,SVG}', GLOB_BRACE);
            $images = [];
            foreach ($files as $file) {
                $images[] = [
                    'name' => basename($file),
                    'size' => filesize($file),
                    'url' => './tileimg/' . basename($file)
                ];
            }
            usort($images, function($a, $b) {
                return strcasecmp($a['name'], $b['name']);
            });
            echo json_encode([
                'status' => 'success',
                'images' => $images
            ]);
            break;

        case 'upload_image':
            if (!isset($_FILES['image']) || $_FILES['image']['error'] !== UPLOAD_ERR_OK) {
                throw new Exception("No file uploaded or upload error.");
            }
            
            $tmpPath = $_FILES['image']['tmp_name'];
            $origName = $_FILES['image']['name'];
            $fileSize = $_FILES['image']['size'];
            
            $imgInfo = getimagesize($tmpPath);
            if ($imgInfo === false) {
                throw new Exception("Uploaded file is not a valid image.");
            }
            
            $dir = __DIR__ . '/tileimg';
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }
            
            $origExt = strtolower(pathinfo($origName, PATHINFO_EXTENSION));
            $baseName = pathinfo($origName, PATHINFO_FILENAME);
            $cleanName = preg_replace('/[^a-zA-Z0-9_\-]/', '_', $baseName);
            
            // 1. If file is <= 50kb (51200 bytes), do not edit or convert it at all
            if ($fileSize <= 51200) {
                $finalName = $cleanName . '.' . $origExt;
                $counter = 1;
                while (file_exists($dir . '/' . $finalName)) {
                    $finalName = $cleanName . '_' . $counter . '.' . $origExt;
                    $counter++;
                }
                $targetPath = $dir . '/' . $finalName;
                if (!move_uploaded_file($tmpPath, $targetPath)) {
                    throw new Exception("Failed to move uploaded image.");
                }
            } else {
                // Otherwise, it gets converted to WebP
                $targetExt = 'webp';
                $finalName = $cleanName . '.' . $targetExt;
                $counter = 1;
                while (file_exists($dir . '/' . $finalName)) {
                    $finalName = $cleanName . '_' . $counter . '.' . $targetExt;
                    $counter++;
                }
                $targetPath = $dir . '/' . $finalName;
                
                // If WebP and <= 100kb, move it unmodified.
                if ($origExt === 'webp' && $fileSize <= 102400) {
                    if (!move_uploaded_file($tmpPath, $targetPath)) {
                        throw new Exception("Failed to move uploaded WebP image.");
                    }
                } else {
                    // Otherwise, use ImageMagick convert command (or GD fallback if needed)
                    $quality = ($fileSize > 102400) ? 40 : 85;
                    $resizeCmd = ($fileSize > 102400) ? '-resize "640x640>"' : '';
                    
                    $escapedTmp = escapeshellarg($tmpPath);
                    $escapedTarget = escapeshellarg($targetPath);
                    
                    $cmd = "/opt/homebrew/bin/convert $escapedTmp $resizeCmd -quality $quality $escapedTarget 2>&1";
                    exec($cmd, $shellOutput, $returnCode);
                    
                    if ($returnCode !== 0) {
                        error_log("ImageMagick failed (code $returnCode): " . implode("\n", $shellOutput) . ". Falling back to GD.");
                        
                        $srcImg = imagecreatefromstring(file_get_contents($tmpPath));
                        if ($srcImg === false) {
                            throw new Exception("Failed to load image for processing.");
                        }
                        
                        $w = imagesx($srcImg);
                        $h = imagesy($srcImg);
                        
                        if ($fileSize > 102400) {
                            $max_w = 640;
                            $max_h = 640;
                            if ($w > $max_w || $h > $max_h) {
                                if ($w > $h) {
                                    $h = floor($h * ($max_w / $w));
                                    $w = $max_w;
                                } else {
                                    $w = floor($w * ($max_h / $h));
                                    $h = $max_h;
                                }
                            }
                            $destImg = imagecreatetruecolor($w, $h);
                            imagealphablending($destImg, false);
                            imagesavealpha($destImg, true);
                            imagecopyresampled($destImg, $srcImg, 0, 0, 0, 0, $w, $h, imagesx($srcImg), imagesy($srcImg));
                            imagewebp($destImg, $targetPath, 40);
                            imagedestroy($destImg);
                        } else {
                            $destImg = imagecreatetruecolor($w, $h);
                            imagealphablending($destImg, false);
                            imagesavealpha($destImg, true);
                            imagecopyresampled($destImg, $srcImg, 0, 0, 0, 0, $w, $h, $w, $h);
                            imagewebp($destImg, $targetPath, 85);
                            imagedestroy($destImg);
                        }
                        imagedestroy($srcImg);
                    }
                }
            }
            
            echo json_encode([
                'status' => 'success',
                'message' => 'Image uploaded successfully.',
                'image' => [
                    'name' => $finalName,
                    'url' => './tileimg/' . $finalName
                ]
            ]);
            break;

        case 'rename_image':
            $oldName = trim($_POST['old_name'] ?? '');
            $newName = trim($_POST['new_name'] ?? '');
            
            if (empty($oldName) || empty($newName)) {
                throw new Exception("Old and new filenames are required.");
            }
            
            $oldExt = strtolower(pathinfo($oldName, PATHINFO_EXTENSION));
            $newNameBase = pathinfo($newName, PATHINFO_FILENAME);
            $newNameClean = preg_replace('/[^a-zA-Z0-9_\-]/', '_', $newNameBase) . '.' . $oldExt;
            
            $oldPath = __DIR__ . '/tileimg/' . $oldName;
            $newPath = __DIR__ . '/tileimg/' . $newNameClean;
            
            if (!file_exists($oldPath)) {
                throw new Exception("Source file does not exist.");
            }
            if (file_exists($newPath)) {
                throw new Exception("A file with the target name already exists.");
            }
            
            $stmt = $db->prepare("SELECT COUNT(*) FROM tiles WHERE background LIKE :pattern");
            $stmt->execute([':pattern' => '%tileimg/' . $oldName . '%']);
            $count = (int)$stmt->fetchColumn();
            
            if ($count > 0) {
                throw new Exception("Renaming rejected: This image is currently in use as a background on $count tile(s).");
            }
            
            if (!rename($oldPath, $newPath)) {
                throw new Exception("Failed to rename file on disk.");
            }
            
            echo json_encode([
                'status' => 'success',
                'message' => 'Image renamed successfully.',
                'new_name' => $newNameClean
            ]);
            break;

        case 'delete_image':
            $name = trim($_POST['name'] ?? '');
            if (empty($name)) {
                throw new Exception("Filename is required.");
            }
            
            $path = __DIR__ . '/tileimg/' . $name;
            if (!file_exists($path)) {
                throw new Exception("File does not exist.");
            }
            
            $stmt = $db->prepare("SELECT COUNT(*) FROM tiles WHERE background LIKE :pattern");
            $stmt->execute([':pattern' => '%tileimg/' . $name . '%']);
            $count = (int)$stmt->fetchColumn();
            
            if ($count > 0) {
                throw new Exception("Deletion rejected: This image is currently in use as a background on $count tile(s).");
            }
            
            if (!unlink($path)) {
                throw new Exception("Failed to delete file from disk.");
            }
            
            echo json_encode([
                'status' => 'success',
                'message' => 'Image deleted successfully.'
            ]);
            break;

        default:
            throw new Exception("Invalid image administration action.");
    }

} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
